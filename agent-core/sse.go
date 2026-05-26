package agentcore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ConnectSSE subscribes to the backend SSE stream on "<topicPrefix>/<deviceID>"
// and invokes onMessage for every dispatched PendingMessage. It reconnects
// automatically with exponential backoff on disconnect and never returns.
func (c *Client) ConnectSSE(topicPrefix string, onMessage func(PendingMessage)) {
	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		err := c.runSSE(topicPrefix, onMessage)
		if err != nil {
			log.Printf("[%s] SSE disconnected: %v, reconnecting in %s", c.deviceID, err, backoff)
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) runSSE(topicPrefix string, onMessage func(PendingMessage)) error {
	sseClient := &http.Client{Timeout: 0}

	req, err := c.NewRequest("GET", "/api/realtime", nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sseClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SSE connect: status %d: %s", resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	topic := topicPrefix + "/" + c.deviceID
	var eventName string

	for scanner.Scan() {
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
				log.Printf("[%s] SSE connected, clientId=%s", c.deviceID, connect.ClientID)

				if err := c.subscribe(connect.ClientID, topic); err != nil {
					return fmt.Errorf("subscribe: %w", err)
				}
				log.Printf("[%s] subscribed to %s", c.deviceID, topic)
				eventName = ""
				continue
			}

			if eventName == topic {
				var msg PendingMessage
				if err := json.Unmarshal([]byte(data), &msg); err != nil {
					log.Printf("[%s] failed to parse SSE message: %v", c.deviceID, err)
					eventName = ""
					continue
				}
				onMessage(msg)
			}

			eventName = ""
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE read: %w", err)
	}
	return fmt.Errorf("SSE stream ended")
}

func (c *Client) subscribe(clientID, topic string) error {
	payload, _ := json.Marshal(map[string]any{
		"clientId":      clientID,
		"subscriptions": []string{topic},
	})

	req, err := c.NewRequest("POST", "/api/realtime", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

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
