package middleware

import (
	"fmt"
	"log/slog"
	"time"
	"vendel/services"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/routine"
	"github.com/pocketbase/pocketbase/tools/types"
)

// lookupByAPIKey finds a record by the hash of the presented key, delegating to
// services.FindByAPIKey (shared with the realtime subscription guard) so the
// hash-with-plaintext-fallback logic lives in exactly one place.
func lookupByAPIKey(app core.App, collection, filter, rawKey string) (*core.Record, error) {
	return services.FindByAPIKey(app, collection, filter, rawKey, nil)
}

// AuthenticateDevice validates the X-API-Key header against sms_devices.
// Returns the device record.
func AuthenticateDevice(e *core.RequestEvent) (*core.Record, error) {
	apiKey := e.Request.Header.Get("X-API-Key")
	if apiKey == "" {
		return nil, fmt.Errorf("missing X-API-Key header")
	}

	record, err := lookupByAPIKey(e.App, "sms_devices", "api_key = {:key}", apiKey)
	if err != nil {
		return nil, fmt.Errorf("invalid device API key")
	}

	return record, nil
}

// AuthenticateIntegrationAPIKey validates the X-API-Key header against api_keys.
// Returns the user ID associated with the API key.
func AuthenticateIntegrationAPIKey(e *core.RequestEvent) (string, error) {
	apiKey := e.Request.Header.Get("X-API-Key")
	if apiKey == "" {
		return "", fmt.Errorf("missing X-API-Key header")
	}

	record, err := lookupByAPIKey(e.App, "api_keys", "key = {:key} && is_active = true", apiKey)
	if err != nil {
		return "", fmt.Errorf("invalid integration API key")
	}

	// Check expiration
	expiresAt := record.GetDateTime("expires_at")
	if !expiresAt.IsZero() && expiresAt.Time().Before(time.Now()) {
		return "", fmt.Errorf("API key expired")
	}

	// Update last_used_at in background
	app := e.App
	routine.FireAndForget(func() {
		record.Set("last_used_at", types.NowDateTime())
		if err := app.Save(record); err != nil {
			app.Logger().Warn("failed to update api_key last_used_at", slog.Any("error", err))
		}
	})

	return record.GetString("user"), nil
}

// IsAppSuperuser checks if the authenticated user has the is_superuser flag.
func IsAppSuperuser(e *core.RequestEvent) bool {
	record := e.Auth
	return record != nil && record.GetBool("is_superuser")
}

// ResolveAuthOrAPIKey tries JWT auth first, falls back to integration API key.
// Returns the user ID.
func ResolveAuthOrAPIKey(e *core.RequestEvent) (string, error) {
	// Try PocketBase JWT auth (set by PB middleware)
	info, _ := e.RequestInfo()
	if info != nil && info.Auth != nil && info.Auth.Id != "" {
		return info.Auth.Id, nil
	}

	// Fall back to integration API key
	return AuthenticateIntegrationAPIKey(e)
}
