package migrations

import (
	"strings"
	"vendel/services"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Hash existing plaintext API keys (sms_devices.api_key and api_keys.key)
// with SHA-256 so a database dump no longer exposes usable bearer
// credentials. Lookups hash the presented key, so clients keep working
// without rotation. Irreversible by design: raw keys are unrecoverable.
func init() {
	m.Register(func(app core.App) error {
		targets := []struct {
			collection string
			field      string
		}{
			{"sms_devices", "api_key"},
			{"api_keys", "key"},
		}

		for _, t := range targets {
			records, err := app.FindAllRecords(t.collection)
			if err != nil {
				// Collection missing (fresh install ordering) — nothing to do.
				continue
			}
			for _, r := range records {
				v := r.GetString(t.field)
				if v == "" || strings.HasPrefix(v, services.APIKeyHashPrefix) {
					continue
				}
				r.Set(t.field, services.HashAPIKey(v))
				if err := app.Save(r); err != nil {
					return err
				}
			}
		}

		return nil
	}, nil)
}
