package migrations

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Ensures a partial unique index on payments.provider_transaction_id so a
// replayed provider webhook cannot insert a duplicate payment record even if
// two requests race past the application-level idempotency check.
//
// The initial migration already defines an identical unique index
// (idx_payments_provider_tx). PocketBase v0.39+ rejects adding a second index
// with the same definition, so this is a no-op when an equivalent unique index
// is already present (the normal case) and only creates one on databases that
// predate the initial index.
func init() {
	m.Register(func(app core.App) error {
		payments, err := app.FindCollectionByNameOrId("payments")
		if err != nil {
			return nil // collection not present (fresh install ordering) — initial migration defines it
		}

		for _, idx := range payments.Indexes {
			if strings.Contains(idx, "provider_transaction_id") &&
				strings.Contains(strings.ToUpper(idx), "UNIQUE") {
				return nil // an equivalent unique index already exists
			}
		}

		payments.AddIndex("idx_payments_provider_tx_id", true, "provider_transaction_id", "provider_transaction_id != ''")

		return app.Save(payments)
	}, func(app core.App) error {
		payments, err := app.FindCollectionByNameOrId("payments")
		if err != nil {
			return nil
		}

		payments.RemoveIndex("idx_payments_provider_tx_id")

		return app.Save(payments)
	})
}
