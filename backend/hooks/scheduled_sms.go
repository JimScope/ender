package hooks

import (
	"fmt"
	"time"
	"vendel/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// scheduledAtSkew tolerates a small clock drift between client and server so
// a "now" submission isn't rejected by a few hundred milliseconds.
const scheduledAtSkew = time.Minute

// RegisterScheduledSMSHooks wires lifecycle hooks for scheduled_sms records:
//   - Validate scheduled_at is in the future on every API request (defense in
//     depth in case the client validation is bypassed). The check lives in
//     the *Request hooks so backend-side saves from the executor cron (which
//     legitimately write to past rows) are not blocked.
//   - Compute next_run_at on create and update for any code path.
//   - Apply quota check on create.
func RegisterScheduledSMSHooks(app *pocketbase.PocketBase) {
	app.OnRecordCreateRequest("scheduled_sms").BindFunc(func(e *core.RecordRequestEvent) error {
		if err := validateScheduledAt(e.Record); err != nil {
			return err
		}
		return e.Next()
	})

	app.OnRecordUpdateRequest("scheduled_sms").BindFunc(func(e *core.RecordRequestEvent) error {
		if err := validateScheduledAt(e.Record); err != nil {
			return err
		}
		return e.Next()
	})

	app.OnRecordCreate("scheduled_sms").BindFunc(func(e *core.RecordEvent) error {
		userId := e.Record.GetString("user")
		if err := services.CheckScheduledSMSQuota(e.App, userId); err != nil {
			return err
		}
		if e.Record.GetString("timezone") == "" {
			e.Record.Set("timezone", "UTC")
		}
		if e.Record.GetString("status") == "" {
			e.Record.Set("status", "active")
		}
		if err := computeNextRunAt(e.Record); err != nil {
			return err
		}
		return e.Next()
	})

	app.OnRecordUpdate("scheduled_sms").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("timezone") == "" {
			e.Record.Set("timezone", "UTC")
		}
		if err := computeNextRunAt(e.Record); err != nil {
			return err
		}
		return e.Next()
	})
}

// validateScheduledAt rejects past datetimes for one_time schedules. Returns
// nil for recurring schedules (cron expression is validated separately).
func validateScheduledAt(record *core.Record) error {
	if record.GetString("schedule_type") != "one_time" {
		return nil
	}
	scheduledAt := record.GetDateTime("scheduled_at")
	if scheduledAt.IsZero() {
		return apis.NewBadRequestError("scheduled_at is required for one_time schedules", nil)
	}
	if scheduledAt.Time().Before(time.Now().Add(-scheduledAtSkew)) {
		return apis.NewBadRequestError("scheduled_at must be in the future", nil)
	}
	return nil
}

// computeNextRunAt sets next_run_at based on schedule_type. Runs on every
// save (including backend-side ones from the executor cron), so it must not
// reject historic scheduled_at values.
func computeNextRunAt(record *core.Record) error {
	scheduleType := record.GetString("schedule_type")
	if scheduleType == "one_time" {
		record.Set("next_run_at", record.GetString("scheduled_at"))
	} else if scheduleType == "recurring" {
		cronExpr := record.GetString("cron_expression")
		tz := record.GetString("timezone")
		nextRun, err := services.ComputeNextRun(cronExpr, tz)
		if err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}
		record.Set("next_run_at", nextRun)
	}
	return nil
}
