package hooks

import (
	"log/slog"
	"vendel/middleware"
	"vendel/services"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// canManageSuperuserFlag reports whether the request is allowed to set or
// modify the is_superuser flag: a real PocketBase superuser (admin dashboard)
// or an authenticated app admin (users record with is_superuser = true).
func canManageSuperuserFlag(e *core.RecordRequestEvent) bool {
	if e.HasSuperuserAuth() {
		return true
	}
	return e.Auth != nil && e.Auth.Collection().Name == "users" && e.Auth.GetBool("is_superuser")
}

// RegisterUserHooks registers hooks for user lifecycle events:
// app_name sync, default quota creation, and cascade deletion.
func RegisterUserHooks(app *pocketbase.PocketBase) {
	// System config: sync app_name to PocketBase settings + invalidate caches
	app.OnRecordAfterUpdateSuccess("system_config").BindFunc(func(e *core.RecordEvent) error {
		key := e.Record.GetString("key")

		if key == "app_name" {
			settings := e.App.Settings()
			settings.Meta.AppName = e.Record.GetString("value")
			settings.Meta.SenderName = e.Record.GetString("value")
			if err := e.App.Save(settings); err != nil {
				e.App.Logger().Warn("could not sync app_name to PocketBase settings", slog.Any("error", err))
			}
		}

		if key == "maintenance_mode" {
			middleware.InvalidateMaintenanceCache()
		}

		return e.Next()
	})

	// Users: create default quota on registration
	app.OnRecordCreate("users").BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return services.CreateDefaultQuota(e.App, e.Record.Id)
	})

	// Users: prevent privilege escalation via is_superuser.
	// Collection rules only gate WHICH record a request may touch, not which
	// FIELDS the payload sets, so the flag must be guarded in request hooks.
	app.OnRecordCreateRequest("users").BindFunc(func(e *core.RecordRequestEvent) error {
		if !canManageSuperuserFlag(e) {
			e.Record.Set("is_superuser", false)
		}
		return e.Next()
	})

	app.OnRecordUpdateRequest("users").BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Record.GetBool("is_superuser") != e.Record.Original().GetBool("is_superuser") &&
			!canManageSuperuserFlag(e) {
			return apis.NewForbiddenError("You are not allowed to change is_superuser", nil)
		}
		return e.Next()
	})

	// Users: cascade delete all related data (GDPR Art. 17)
	// IMPORTANT: cascade MUST run before e.Next() because related
	// collections have Required:true relations to users — PocketBase
	// blocks the user delete if any referencing records still exist.
	app.OnRecordDelete("users").BindFunc(func(e *core.RecordEvent) error {
		userId := e.Record.Id

		// Delete records that reference users directly.
		// contacts must go before contact_groups (contacts reference groups).
		collections := []string{
			"sms_messages", "sms_devices", "webhook_configs",
			"api_keys", "user_quotas", "sms_templates", "scheduled_sms",
			"contacts", "contact_groups", "user_balances", "promo_redemptions",
		}
		for _, col := range collections {
			records, err := e.App.FindRecordsByFilter(col, "user = {:uid}", "", 0, 0, dbx.Params{"uid": userId})
			if err != nil {
				e.App.Logger().Warn("cascade delete: failed to list records",
					slog.String("collection", col), slog.Any("error", err))
				continue
			}
			for _, r := range records {
				if col == "webhook_configs" {
					logs, _ := e.App.FindRecordsByFilter("webhook_delivery_logs", "webhook = {:wid}", "", 0, 0, dbx.Params{"wid": r.Id})
					for _, l := range logs {
						if err := e.App.Delete(l); err != nil {
							e.App.Logger().Warn("cascade delete: failed to delete webhook log",
								slog.String("record", l.Id), slog.Any("error", err))
						}
					}
				}
				if err := e.App.Delete(r); err != nil {
					e.App.Logger().Warn("cascade delete: failed to delete record",
						slog.String("collection", col), slog.String("record", r.Id), slog.Any("error", err))
				}
			}
		}

		// Delete subscriptions and their payments (payments link via subscription, not user)
		subscriptions, err := e.App.FindRecordsByFilter("subscriptions", "user = {:uid}", "", 0, 0, dbx.Params{"uid": userId})
		if err == nil {
			for _, sub := range subscriptions {
				payments, _ := e.App.FindRecordsByFilter("payments", "subscription = {:sid}", "", 0, 0, dbx.Params{"sid": sub.Id})
				for _, p := range payments {
					_ = e.App.Delete(p)
				}
				_ = e.App.Delete(sub)
			}
		}

		e.App.Logger().Info("Cascade deleted user data", slog.String("user", userId))
		return e.Next()
	})
}
