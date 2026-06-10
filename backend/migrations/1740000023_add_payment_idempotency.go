package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds a dedicated payment_idempotency collection recording which external
// provider transactions have already been credited to a user's balance.
//
// Previously creditAndActivate inserted this marker into the payments
// collection, but payments.subscription is Required and balance/deposit
// credits have no subscription, so the marker failed validation and rolled
// back the entire credit transaction — every real top-up/deposit failed to
// credit the user. Isolating idempotency here removes that coupling.
func init() {
	m.Register(func(app core.App) error {
		col := core.NewBaseCollection("payment_idempotency")
		col.Fields.Add(
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.TextField{Name: "provider_transaction_id", Required: true, Max: 255},
			&core.NumberField{Name: "amount"},
			&core.TextField{Name: "currency", Max: 10},
			&core.TextField{Name: "provider", Max: 50},
		)
		// Written exclusively by creditAndActivate (services) — no public access.
		col.ListRule = nil
		col.ViewRule = nil
		col.CreateRule = nil
		col.UpdateRule = nil
		col.DeleteRule = nil
		// Unique so a replayed provider webhook cannot double-credit even if two
		// deliveries race past the application-level idempotency check.
		col.AddIndex("idx_payment_idempotency_tx", true, "provider_transaction_id", "")

		return app.Save(col)
	}, func(app core.App) error {
		col, err := app.FindCollectionByNameOrId("payment_idempotency")
		if err != nil {
			return nil
		}
		return app.Delete(col)
	})
}
