package handlers

import (
	"net/http"
	"vendel/middleware"
	"vendel/services"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterSMPPRoutes registers custom SMPP API routes.
func RegisterSMPPRoutes(se *core.ServeEvent) {
	se.Router.GET("/api/smpp/config", handleSMPPConfig)
}

// handleSMPPConfig returns the SMPP bind configuration for the authenticated
// device (auth: device API key). The password is returned in plaintext so the
// agent can bind against the SMSC.
func handleSMPPConfig(e *core.RequestEvent) error {
	device, err := middleware.AuthenticateDevice(e)
	if err != nil {
		return apis.NewUnauthorizedError("Invalid API key", nil)
	}

	if device.GetString("device_type") != "smpp" {
		return apis.NewBadRequestError("device is not an SMPP device", nil)
	}

	cfg, err := e.App.FindFirstRecordByFilter(
		"smpp_configs",
		"device = {:device}",
		dbx.Params{"device": device.Id},
	)
	if err != nil {
		return apis.NewNotFoundError("SMPP configuration not found for this device", nil)
	}

	password, err := services.DecryptSecret(cfg.GetString("password"))
	if err != nil {
		return apis.NewApiError(http.StatusInternalServerError, "failed to decrypt SMPP password", nil)
	}

	bindMode := cfg.GetString("bind_mode")
	if bindMode == "" {
		bindMode = "trx"
	}

	return e.JSON(http.StatusOK, map[string]any{
		"device_id":            device.Id,
		"host":                 cfg.GetString("host"),
		"port":                 cfg.GetInt("port"),
		"system_id":            cfg.GetString("system_id"),
		"password":             password,
		"system_type":          cfg.GetString("system_type"),
		"bind_mode":            bindMode,
		"source_ton":           cfg.GetInt("source_ton"),
		"source_npi":           cfg.GetInt("source_npi"),
		"dest_ton":             cfg.GetInt("dest_ton"),
		"dest_npi":             cfg.GetInt("dest_npi"),
		"use_tls":              cfg.GetBool("use_tls"),
		"enquire_link_seconds": cfg.GetInt("enquire_link_seconds"),
		"default_data_coding":  cfg.GetInt("default_data_coding"),
		"submit_throttle_tps":  cfg.GetInt("submit_throttle_tps"),
		"source_addr":          device.GetString("phone_number"),
	})
}
