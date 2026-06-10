package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds a partial unique index on payments.provider_transaction_id so a
// replayed provider webhook cannot insert a duplicate payment record even
// if two requests race past the application-level idempotency check.
// Partial (!= '') because manual/balance payments may have no provider tx id.
func init() {
	m.Register(func(app core.App) error {
		payments, err := app.FindCollectionByNameOrId("payments")
		if err != nil {
			return nil // collection not present (fresh install ordering) — initial migration defines it
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
