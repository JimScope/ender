package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/cron"
)

// ProcessDueSchedules finds and dispatches all scheduled SMS that are due.
func ProcessDueSchedules(app core.App) error {
	now := FilterNow()

	records, err := app.FindRecordsByFilter(
		"scheduled_sms",
		"status = 'active' && next_run_at != '' && next_run_at <= {:now}",
		"", 50, 0,
		dbx.Params{"now": now},
	)
	if err != nil {
		return err
	}

	if len(records) == 0 {
		return nil
	}

	dispatched := 0
	failed := 0
	for _, record := range records {
		// Decode recipients
		var recipients []string
		recipientsJSON := record.GetString("recipients")
		if err := json.Unmarshal([]byte(recipientsJSON), &recipients); err != nil {
			app.Logger().Warn("scheduled SMS: invalid recipients JSON",
				slog.String("id", record.Id), slog.Any("error", err))
			failed++
			continue
		}

		userId := record.GetString("user")
		body := GetRecordBody(record)
		deviceId := record.GetString("device_id")

		// Send the SMS
		_, err := SendSMS(app, userId, recipients, body, deviceId, nil)
		if err != nil {
			app.Logger().Warn("scheduled SMS: send failed",
				slog.String("id", record.Id), slog.Any("error", err))
			failed++
			continue
		}

		// Update record after successful send
		record.Set("last_run_at", time.Now().UTC().Format(time.RFC3339))

		scheduleType := record.GetString("schedule_type")
		if scheduleType == "one_time" {
			record.Set("status", "completed")
			record.Set("next_run_at", "")
		} else if scheduleType == "recurring" {
			cronExpr := record.GetString("cron_expression")
			tz := record.GetString("timezone")
			if tz == "" {
				tz = "UTC"
			}
			nextRun, err := ComputeNextRun(cronExpr, tz)
			if err != nil {
				app.Logger().Warn("scheduled SMS: failed to compute next run",
					slog.String("id", record.Id), slog.Any("error", err))
			} else {
				record.Set("next_run_at", nextRun)
			}
		}

		if err := app.Save(record); err != nil {
			app.Logger().Warn("scheduled SMS: failed to update record",
				slog.String("id", record.Id), slog.Any("error", err))
			failed++
			continue
		}
		dispatched++
	}

	if dispatched > 0 || failed > 0 {
		app.Logger().Info("Processed scheduled SMS",
			slog.Int("dispatched", dispatched), slog.Int("failed", failed))
	}

	return nil
}

// ComputeNextRun calculates the next occurrence of a cron expression in the
// given IANA timezone. Returns a UTC RFC3339 string.
func ComputeNextRun(cronExpr, timezone string) (string, error) {
	schedule, err := cron.NewSchedule(cronExpr)
	if err != nil {
		return "", fmt.Errorf("invalid cron expression: %w", err)
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return "", fmt.Errorf("invalid timezone: %w", err)
	}

	// Start from the next minute
	now := time.Now().In(loc)
	candidate := now.Truncate(time.Minute).Add(time.Minute)

	// Cap at 366 days to avoid infinite loops
	limit := candidate.Add(366 * 24 * time.Hour)
	for candidate.Before(limit) {
		if schedule.IsDue(cron.NewMoment(candidate)) {
			return candidate.UTC().Format(time.RFC3339), nil
		}
		candidate = candidate.Add(time.Minute)
	}

	return "", fmt.Errorf("no matching time found within 366 days")
}

// ValidateCronExpression checks if a cron expression is valid.
func ValidateCronExpression(cronExpr string) error {
	_, err := cron.NewSchedule(cronExpr)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}
