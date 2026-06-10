package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// sseIdleTimeout bounds how long a silent SSE connection is trusted.
// PocketBase closes idle realtime connections after 5 minutes (and recycles
// active ones after 30), so a healthy stream never stays quiet longer than
// that — 6 minutes of total silence means the TCP connection died without
// a FIN (e.g. NAT table drop) and we must reconnect ourselves.
const sseIdleTimeout = 6 * time.Minute

// reportRetryDelays paces re-attempts of status/incoming reports. A sent
// message whose report is lost gets re-delivered by the backend retry cron
// → duplicate SMS for the recipient, so it is worth insisting for ~1 min.
var reportRetryDelays = []time.Duration{0, 2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}

// PendingMessage represents an SMS message to be sent.
type PendingMessage struct {
	MessageID string `json:"message_id"`
	Recipient string `json:"recipient"`
	Body      string `json:"body"`
}

// VendelClient communicates with the Vendel backend.
type VendelClient struct {
	baseURL  string
	apiKey   string
	deviceID string // resolved from backend via FetchPending
	http     *http.Client
	sse      *http.Client
}

// NewVendelClient creates a new Vendel API client.
// The deviceID is resolved from the backend on the first call to FetchPending.
func NewVendelClient(baseURL, apiKey string) *VendelClient {
	return &VendelClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		sse: &http.Client{
			Timeout: 0, // the stream is long-lived; reads are bounded by the idle watchdog
			Transport: &http.Transport{
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
	}
}

// FetchPending claims messages assigned while the agent was offline.
// It also resolves the device record ID from the backend (stored in
// c.deviceID). NOTE: the backend marks returned messages as "sending", so
// the caller must enqueue them — a discarded batch would strand them until
// the backend retry cron rescues them.
func (c *VendelClient) FetchPending(ctx context.Context) ([]PendingMessage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/sms/pending", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch pending: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch pending: status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		DeviceID string           `json:"device_id"`
		Messages []PendingMessage `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode pending response: %w", err)
	}

	if result.DeviceID != "" {
		c.deviceID = result.DeviceID
	}

	return result.Messages, nil
}

// ReportStatus reports message delivery status back to the server.
func (c *VendelClient) ReportStatus(ctx context.Context, messageID, status, errorMessage string) error {
	payload, _ := json.Marshal(map[string]string{
		"message_id":    messageID,
		"status":        status,
		"error_message": errorMessage,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/sms/report", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("report status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("report status: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ReportIncoming reports an incoming SMS received on the modem.
func (c *VendelClient) ReportIncoming(ctx context.Context, fromNumber, body, timestamp string) error {
	payload, _ := json.Marshal(map[string]string{
		"from_number": fromNumber,
		"body":        body,
		"timestamp":   timestamp,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/sms/incoming", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("report incoming: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("report incoming: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// reportWithRetry retries a report call with increasing delays. Returns the
// last error if every attempt failed; the caller decides how loudly to log.
func reportWithRetry(ctx context.Context, fn func() error) error {
	var err error
	for _, delay := range reportRetryDelays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if err = fn(); err == nil {
			return nil
		}
	}
	return err
}

// RunSSE keeps an SSE connection to PocketBase alive until ctx is canceled,
// reconnecting with exponential backoff. onConnect fires after each
// successful subscription (initial and reconnects) — the caller uses it to
// recover messages assigned while disconnected. onMessage fires for each
// assignment event and must not block the read loop.
func (c *VendelClient) RunSSE(ctx context.Context, onConnect func(), onMessage func(PendingMessage)) {
	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		start := time.Now()
		err := c.runSSE(ctx, onConnect, onMessage)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[%s] SSE disconnected: %v, reconnecting in %s", c.deviceID, err, backoff)
		}

		// PocketBase recycles idle SSE connections every ~5 minutes by
		// design, so routine disconnects after a long-lived connection must
		// not escalate the backoff.
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *VendelClient) runSSE(ctx context.Context, onConnect func(), onMessage func(PendingMessage)) error {
	// A dedicated sub-context lets the idle watchdog kill just this
	// connection attempt (the canceled request unblocks scanner.Scan).
	sseCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(sseCtx, "GET", c.baseURL+"/api/realtime", nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.sse.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SSE connect: status %d: %s", resp.StatusCode, body)
	}

	// Idle watchdog: cancel the request if the server stays silent past
	// the PocketBase idle window — that means the TCP connection is dead.
	watchdog := time.AfterFunc(sseIdleTimeout, cancel)
	defer watchdog.Stop()

	// Parse SSE stream to get clientId from the PB_CONNECT event
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var clientID string
	var eventName string

	for scanner.Scan() {
		watchdog.Reset(sseIdleTimeout)
		line := scanner.Text()

		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

			if eventName == "PB_CONNECT" {
				var connect struct {
					ClientID string `json:"clientId"`
				}
				if err := json.Unmarshal([]byte(data), &connect); err != nil {
					return fmt.Errorf("parse PB_CONNECT: %w", err)
				}
				clientID = connect.ClientID
				log.Printf("[%s] SSE connected, clientId=%s", c.deviceID, clientID)

				// Step 2: Subscribe to our modem topic
				if err := c.subscribe(sseCtx, clientID); err != nil {
					return fmt.Errorf("subscribe: %w", err)
				}
				log.Printf("[%s] subscribed to modem/%s", c.deviceID, c.deviceID)
				if onConnect != nil {
					onConnect()
				}
				continue
			}

			// Handle modem message events
			topic := "modem/" + c.deviceID
			if eventName == topic {
				var msg PendingMessage
				if err := json.Unmarshal([]byte(data), &msg); err != nil {
					log.Printf("[%s] failed to parse SSE message: %v", c.deviceID, err)
					continue
				}
				onMessage(msg)
			}

			eventName = ""
			continue
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if sseCtx.Err() != nil {
		return fmt.Errorf("SSE idle for %s, assuming dead connection", sseIdleTimeout)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE read: %w", err)
	}
	return fmt.Errorf("SSE stream ended")
}

// subscribe sends a POST to /api/realtime to register subscriptions.
func (c *VendelClient) subscribe(ctx context.Context, clientID string) error {
	topic := "modem/" + c.deviceID
	payload, _ := json.Marshal(map[string]any{
		"clientId":      clientID,
		"subscriptions": []string{topic},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/realtime", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("subscribe failed: status %d: %s", resp.StatusCode, body)
	}
	return nil
}
