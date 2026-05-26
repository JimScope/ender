package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	agentcore "github.com/JimScope/vendel/agent-core"
)

// RemoteSMPPConfig is the SMPP bind configuration fetched from the backend.
type RemoteSMPPConfig struct {
	DeviceID           string `json:"device_id"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	SystemID           string `json:"system_id"`
	Password           string `json:"password"`
	SystemType         string `json:"system_type"`
	BindMode           string `json:"bind_mode"`
	SourceTON          int    `json:"source_ton"`
	SourceNPI          int    `json:"source_npi"`
	DestTON            int    `json:"dest_ton"`
	DestNPI            int    `json:"dest_npi"`
	UseTLS             bool   `json:"use_tls"`
	EnquireLinkSeconds int    `json:"enquire_link_seconds"`
	DefaultDataCoding  int    `json:"default_data_coding"`
	SubmitThrottleTPS  int    `json:"submit_throttle_tps"`
	SourceAddr         string `json:"source_addr"`
}

// FetchSMPPConfig retrieves the SMPP bind config from the backend. As a side
// effect it seeds the agent-core client's device ID so SSE subscriptions can
// proceed before FetchPending is called.
func FetchSMPPConfig(c *agentcore.Client) (*RemoteSMPPConfig, error) {
	req, err := c.NewRequest("GET", "/api/smpp/config", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch smpp config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch smpp config: status %d: %s", resp.StatusCode, body)
	}

	var cfg RemoteSMPPConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode smpp config: %w", err)
	}
	if cfg.DeviceID != "" {
		c.SetDeviceID(cfg.DeviceID)
	}
	return &cfg, nil
}
