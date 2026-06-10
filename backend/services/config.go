package services

import (
	"log/slog"
	"strings"
	"vendel/services/payment"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// GetSystemConfigValue reads a value from the system_config collection.
// Values stored encrypted (enc: prefix) are transparently decrypted;
// plaintext values pass through unchanged.
func GetSystemConfigValue(app core.App, key string) string {
	record, err := app.FindFirstRecordByFilter(
		"system_config",
		"key = {:key}",
		dbx.Params{"key": key},
	)
	if err != nil || record == nil {
		return ""
	}

	value, err := DecryptSecret(record.GetString("value"))
	if err != nil {
		app.Logger().Error("failed to decrypt system_config value (check WEBHOOK_ENCRYPTION_KEY)",
			slog.String("key", key), slog.Any("error", err))
		return ""
	}
	return value
}

// GetAppSettings returns public app settings.
func GetAppSettings(app core.App) map[string]any {
	keys := []string{"app_name", "support_email", "banner_text", "banner_url"}
	result := make(map[string]any)
	for _, k := range keys {
		result[k] = GetSystemConfigValue(app, k)
	}
	// Fill defaults
	if result["app_name"] == "" {
		result["app_name"] = DefaultAppName
	}
	// Add maintenance status
	if strings.ToLower(GetSystemConfigValue(app, "maintenance_mode")) == "true" {
		result["maintenance_mode"] = "true"
	} else {
		result["maintenance_mode"] = "false"
	}
	// Add payment providers that are enabled via system_config toggle.
	// Each provider needs provider_<name>_enabled = "true" in system_config.
	allProviders := []struct{ name, displayName string }{
		{"trondealer", "TronDealer"},
		{"qvapay", "QvaPay"},
		{"stripe", "Stripe"},
	}
	resolve := func(key string) string { return GetSystemConfigValue(app, key) }
	var providers []map[string]string
	for _, p := range allProviders {
		enabledKey := "provider_" + p.name + "_enabled"
		if strings.ToLower(GetSystemConfigValue(app, enabledKey)) != "true" {
			continue
		}
		// Only list the provider if it's actually configured (has API keys)
		provider := payment.GetProviderWithConfig(p.name, resolve)
		if provider == nil {
			continue
		}
		providers = append(providers, map[string]string{
			"name":         p.name,
			"display_name": p.displayName,
		})
	}
	result["payment_providers"] = providers
	return result
}
