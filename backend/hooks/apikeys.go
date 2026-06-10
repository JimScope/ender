package hooks

import (
	"time"
	"vendel/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterApiKeyHooks generates a secure key and sets a default 1-year
// expiration on API key creation. Only the SHA-256 hash of the key is
// persisted; the raw value is swapped into the response after the save so
// the caller can read it exactly once.
func RegisterApiKeyHooks(app *pocketbase.PocketBase) {
	app.OnRecordCreate("api_keys").BindFunc(func(e *core.RecordEvent) error {
		rawKey := services.GenerateSecureKey(services.APIKeyPrefix, services.GeneratedKeyLen)
		e.Record.Set("key", services.HashAPIKey(rawKey))

		// Default expiration to 1 year if not explicitly set
		if e.Record.GetDateTime("expires_at").IsZero() {
			e.Record.Set("expires_at", time.Now().AddDate(services.DefaultAPIKeyExpiryYrs, 0, 0).UTC().Format(time.RFC3339))
		}

		if err := e.Next(); err != nil {
			return err
		}

		// The hash is now persisted. Expose the raw key in the API response
		// (shown once); "key" is hidden by default so it must be unhidden.
		e.Record.Set("key", rawKey)
		e.Record.Unhide("key")
		return nil
	})
}
