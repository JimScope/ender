// Package agentcore is the shared HTTP + SSE client used by Vendel's device
// agents (modem-agent, smpp-agent, …). Each agent embeds *Client and adds its
// own transport-specific code on top.
package agentcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client communicates with the Vendel backend over HTTP and SSE.
// The device ID is resolved from the backend on the first call to FetchPending.
type Client struct {
	baseURL  string
	apiKey   string
	deviceID string
	http     *http.Client
}

// New creates a new Vendel API client.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// DeviceID returns the device record ID resolved from the backend. Empty until
// the first successful call to FetchPending (or until the backend emits the
// device_id on the /api/smpp/config response, which SMPP agents assign
// via SetDeviceID).
func (c *Client) DeviceID() string { return c.deviceID }

// SetDeviceID lets transport-specific clients seed the device ID from other
// endpoints (e.g. GET /api/smpp/config) before FetchPending is called.
func (c *Client) SetDeviceID(id string) { c.deviceID = id }

// BaseURL returns the sanitized backend base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// APIKey returns the configured device API key.
func (c *Client) APIKey() string { return c.apiKey }

// HTTP returns the underlying HTTP client so transport-specific callers can
// reuse it for their own endpoints.
func (c *Client) HTTP() *http.Client { return c.http }

// NewRequest builds an authenticated request targeting the backend. Callers
// must still set Content-Type for POST bodies.
func (c *Client) NewRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	return req, nil
}

// FetchPending claims messages assigned while the agent was offline and
// resolves the device record ID as a side effect.
func (c *Client) FetchPending() ([]PendingMessage, error) {
	req, err := c.NewRequest("GET", "/api/sms/pending", nil)
	if err != nil {
		return nil, err
	}

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
func (c *Client) ReportStatus(messageID, status, errorMessage string) error {
	payload, _ := json.Marshal(map[string]string{
		"message_id":    messageID,
		"status":        status,
		"error_message": errorMessage,
	})

	req, err := c.NewRequest("POST", "/api/sms/report", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

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

// ReportIncoming reports an incoming SMS (MO) received by the agent.
func (c *Client) ReportIncoming(fromNumber, body, timestamp string) error {
	payload, _ := json.Marshal(map[string]string{
		"from_number": fromNumber,
		"body":        body,
		"timestamp":   timestamp,
	})

	req, err := c.NewRequest("POST", "/api/sms/incoming", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("report incoming: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("report incoming: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
