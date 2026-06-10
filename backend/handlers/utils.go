package handlers

import (
	"vendel/middleware"
	"vendel/services"
	"net/http"
	"os"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// paymentSecretKeys are system_config keys holding payment provider
// credentials. They are stored AES-GCM encrypted and never returned by the
// API (GET strips them; admins use env_hints to know whether they are set).
var paymentSecretKeys = map[string]bool{
	"trondealer_api_key":        true,
	"trondealer_webhook_secret": true,
	"qvapay_app_secret":         true,
	"stripe_secret_key":         true,
	"stripe_webhook_secret":     true,
}

// encryptIfSecret encrypts the value when the key is a payment secret.
// Returns an error if encryption is unavailable (missing encryption key) —
// secrets must never be persisted in plaintext.
func encryptIfSecret(key, value string) (string, error) {
	if !paymentSecretKeys[key] || value == "" {
		return value, nil
	}
	return services.EncryptSecret(value)
}

// RegisterUtilRoutes registers utility routes.
// Note: /api/health is provided by PocketBase out of the box.
func RegisterUtilRoutes(se *core.ServeEvent) {
	// GET /api/utils/app-settings
	se.Router.GET("/api/utils/app-settings", func(e *core.RequestEvent) error {
		settings := services.GetAppSettings(e.App)
		return e.JSON(http.StatusOK, settings)
	})

	// GET /api/user/export — export all user data (GDPR Art. 20)
	se.Router.GET("/api/user/export", func(e *core.RequestEvent) error {
		data, err := services.ExportUserData(e.App, e.Auth.Id)
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "Failed to export data", nil)
		}

		data["exported_at"] = time.Now().UTC().Format(time.RFC3339)

		e.Response.Header().Set("Content-Disposition", "attachment; filename=\"vendel-export.json\"")
		return e.JSON(http.StatusOK, data)
	}).Bind(apis.RequireAuth("users"))

	// GET /api/system-config — returns all system_config records (app admin only)
	// Also includes env_hints: which config keys have env vars set.
	se.Router.GET("/api/system-config", func(e *core.RequestEvent) error {
		if !middleware.IsAppSuperuser(e) {
			return e.ForbiddenError("", nil)
		}
		records, err := e.App.FindAllRecords("system_config")
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "Failed to fetch system config", nil)
		}

		// Map config keys → env var names
		envMap := map[string]string{
			"trondealer_api_key":        "TRONDEALER_API_KEY",
			"trondealer_webhook_secret": "TRONDEALER_WEBHOOK_SECRET",
			"qvapay_app_id":             "QVAPAY_APP_ID",
			"qvapay_app_secret":         "QVAPAY_APP_SECRET",
			"stripe_secret_key":         "STRIPE_SECRET_KEY",
			"stripe_webhook_secret":     "STRIPE_WEBHOOK_SECRET",
		}
		envHints := map[string]bool{}
		for configKey, envKey := range envMap {
			envHints[configKey] = os.Getenv(envKey) != ""
		}

		// Strip secret values — never return them, admin uses env_hints to know if set
		for _, r := range records {
			if paymentSecretKeys[r.GetString("key")] {
				r.Set("value", "")
			}
		}

		return e.JSON(http.StatusOK, map[string]any{"data": records, "env_hints": envHints})
	}).Bind(apis.RequireAuth("users"))

	// PUT /api/system-config — batch update config values (app admin only)
	se.Router.PUT("/api/system-config", func(e *core.RequestEvent) error {
		if !middleware.IsAppSuperuser(e) {
			return e.ForbiddenError("", nil)
		}

		var body map[string]string
		if err := e.BindBody(&body); err != nil {
			return apis.NewBadRequestError("Invalid request body", nil)
		}

		collection, err := e.App.FindCollectionByNameOrId("system_config")
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "system_config collection not found", nil)
		}

		for key, value := range body {
			// Don't overwrite secrets with empty values (GET strips them)
			if value == "" && paymentSecretKeys[key] {
				continue
			}
			value, err := encryptIfSecret(key, value)
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError,
					"Cannot store secret: WEBHOOK_ENCRYPTION_KEY is not configured", nil)
			}
			record, _ := e.App.FindFirstRecordByFilter("system_config", "key = {:key}", map[string]any{"key": key})
			if record != nil {
				record.Set("value", value)
			} else {
				record = core.NewRecord(collection)
				record.Set("key", key)
				record.Set("value", value)
			}
			if err := e.App.Save(record); err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Failed to save config key: "+key, nil)
			}
		}

		return e.JSON(http.StatusOK, map[string]any{"updated": len(body)})
	}).Bind(apis.RequireAuth("users"))

	// PATCH /api/system-config/{key} — update (or create) a config value (app admin only)
	se.Router.PATCH("/api/system-config/{key}", func(e *core.RequestEvent) error {
		if !middleware.IsAppSuperuser(e) {
			return e.ForbiddenError("", nil)
		}
		key := e.Request.PathValue("key")

		var body struct {
			Value string `json:"value"`
		}
		if err := e.BindBody(&body); err != nil {
			return apis.NewBadRequestError("Invalid request body", nil)
		}

		value, err := encryptIfSecret(key, body.Value)
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError,
				"Cannot store secret: WEBHOOK_ENCRYPTION_KEY is not configured", nil)
		}

		// Try to find existing record by key
		record, _ := e.App.FindFirstRecordByFilter("system_config", "key = {:key}", map[string]any{"key": key})

		if record != nil {
			// Update existing
			record.Set("value", value)
			if err := e.App.Save(record); err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Failed to update config", nil)
			}
		} else {
			// Create new
			collection, err := e.App.FindCollectionByNameOrId("system_config")
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "system_config collection not found", nil)
			}
			record = core.NewRecord(collection)
			record.Set("key", key)
			record.Set("value", value)
			if err := e.App.Save(record); err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Failed to create config", nil)
			}
		}

		// Never echo secret material back (matches GET behavior)
		if paymentSecretKeys[key] {
			record.Set("value", "")
		}

		return e.JSON(http.StatusOK, record)
	}).Bind(apis.RequireAuth("users"))

	// POST /api/sms/validate-cron — Validate a cron expression (auth: JWT)
	se.Router.POST("/api/sms/validate-cron", func(e *core.RequestEvent) error {
		var body struct {
			Expression string `json:"expression"`
			Timezone   string `json:"timezone"`
		}
		if err := e.BindBody(&body); err != nil {
			return apis.NewBadRequestError("Invalid request body", nil)
		}

		if body.Expression == "" {
			return e.JSON(http.StatusOK, map[string]any{
				"valid": false,
				"error": "Expression is required",
			})
		}

		if body.Timezone == "" {
			body.Timezone = "UTC"
		}

		if err := services.ValidateCronExpression(body.Expression); err != nil {
			return e.JSON(http.StatusOK, map[string]any{
				"valid": false,
				"error": err.Error(),
			})
		}

		nextRun, err := services.ComputeNextRun(body.Expression, body.Timezone)
		if err != nil {
			return e.JSON(http.StatusOK, map[string]any{
				"valid": false,
				"error": err.Error(),
			})
		}

		return e.JSON(http.StatusOK, map[string]any{
			"valid":    true,
			"next_run": nextRun,
		})
	}).Bind(apis.RequireAuth("users"))

}
