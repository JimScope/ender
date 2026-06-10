package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// QuotaError is returned when a quota limit is exceeded.
type QuotaError struct {
	StatusCode int
	Body       map[string]any
}

func (e *QuotaError) Error() string {
	b, _ := json.Marshal(e.Body)
	return string(b)
}

// GetUserQuota returns quota info for a user.
func GetUserQuota(app core.App, userId string) (map[string]any, error) {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return nil, err
	}

	plan, err := app.FindRecordById("user_plans", quota.GetString("plan"))
	if err != nil {
		return nil, fmt.Errorf("plan not found")
	}

	// Calculate reset date: 30 days after last_reset_date for all users
	var resetDate string
	lastReset := quota.GetDateTime("last_reset_date")
	if !lastReset.IsZero() {
		resetDate = lastReset.Time().AddDate(0, 0, 30).Format("2006-01-02")
	}

	scheduledCount, _ := countActiveScheduledSMS(app, userId)
	integrationCount, _ := countIntegrations(app, userId)

	return map[string]any{
		"plan":                plan.GetString("name"),
		"sms_sent_this_month": quota.GetInt("sms_sent_this_month"),
		"max_sms_per_month":   plan.GetInt("max_sms_per_month"),
		"devices_registered":  quota.GetInt("devices_registered"),
		"max_devices":         plan.GetInt("max_devices"),
		"reset_date":          resetDate,
		"scheduled_sms_count": scheduledCount,
		"max_scheduled_sms":   plan.GetInt("max_scheduled_sms"),
		"integrations_count":  integrationCount,
		"max_integrations":    plan.GetInt("max_integrations"),
	}, nil
}

// CheckDeviceQuota verifies the user can register another device.
func CheckDeviceQuota(app core.App, userId string) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	plan, err := app.FindRecordById("user_plans", quota.GetString("plan"))
	if err != nil {
		return fmt.Errorf("plan not found")
	}

	registered := quota.GetInt("devices_registered")
	limit := plan.GetInt("max_devices")

	if registered >= limit {
		return &QuotaError{
			StatusCode: 429,
			Body: map[string]any{
				"detail":      fmt.Sprintf("Device limit of %d reached", limit),
				"error":       "quota_exceeded",
				"quota_type":  "devices",
				"limit":       limit,
				"used":        registered,
				"available":   0,
				"upgrade_url": "/api/plans/upgrade",
			},
		}
	}

	return nil
}

// CheckScheduledSMSQuota verifies the user can create another scheduled SMS.
func CheckScheduledSMSQuota(app core.App, userId string) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	plan, err := app.FindRecordById("user_plans", quota.GetString("plan"))
	if err != nil {
		return fmt.Errorf("plan not found")
	}

	used, err := countActiveScheduledSMS(app, userId)
	if err != nil {
		return err
	}

	limit := plan.GetInt("max_scheduled_sms")
	if used >= limit {
		return &QuotaError{
			StatusCode: 429,
			Body: map[string]any{
				"detail":      fmt.Sprintf("Scheduled SMS limit of %d reached", limit),
				"error":       "quota_exceeded",
				"quota_type":  "scheduled_sms",
				"limit":       limit,
				"used":        used,
				"available":   0,
				"upgrade_url": "/api/plans/upgrade",
			},
		}
	}

	return nil
}

// CheckIntegrationQuota verifies the user can create another webhook integration.
func CheckIntegrationQuota(app core.App, userId string) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	plan, err := app.FindRecordById("user_plans", quota.GetString("plan"))
	if err != nil {
		return fmt.Errorf("plan not found")
	}

	used, err := countIntegrations(app, userId)
	if err != nil {
		return err
	}

	limit := plan.GetInt("max_integrations")
	if used >= limit {
		return &QuotaError{
			StatusCode: 429,
			Body: map[string]any{
				"detail":      fmt.Sprintf("Integration limit of %d reached", limit),
				"error":       "quota_exceeded",
				"quota_type":  "integrations",
				"limit":       limit,
				"used":        used,
				"available":   0,
				"upgrade_url": "/api/plans/upgrade",
			},
		}
	}

	return nil
}

// ReserveSMSQuota atomically increments the monthly SMS counter only when
// the user stays within the plan limit, closing the check-then-increment
// race (concurrent sends could otherwise all pass the check and overshoot).
// Returns a QuotaError when the reservation does not fit.
func ReserveSMSQuota(app core.App, userId string, count int) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	plan, err := app.FindRecordById("user_plans", quota.GetString("plan"))
	if err != nil {
		return fmt.Errorf("plan not found")
	}
	limit := plan.GetInt("max_sms_per_month")

	// Conditional UPDATE: the WHERE clause re-checks the limit at write time,
	// so two concurrent reservations can never jointly exceed it.
	res, err := app.DB().
		NewQuery(`UPDATE user_quotas
			SET sms_sent_this_month = sms_sent_this_month + {:count}
			WHERE id = {:id} AND sms_sent_this_month + {:count} <= {:limit}`).
		Bind(dbx.Params{"count": count, "id": quota.Id, "limit": limit}).
		Execute()
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		sent := quota.GetInt("sms_sent_this_month")
		available := max(limit-sent, 0)
		return &QuotaError{
			StatusCode: 429,
			Body: map[string]any{
				"detail":      fmt.Sprintf("You can only send %d more SMS this month", available),
				"error":       "quota_exceeded",
				"quota_type":  "sms_monthly",
				"limit":       limit,
				"used":        sent,
				"available":   available,
				"upgrade_url": "/api/plans/upgrade",
			},
		}
	}
	return nil
}

// ReleaseSMSQuota returns a previously reserved amount to the user's monthly
// quota (used when message creation fails after the reservation).
func ReleaseSMSQuota(app core.App, userId string, count int) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	_, err = app.DB().
		NewQuery("UPDATE user_quotas SET sms_sent_this_month = MAX(sms_sent_this_month - {:count}, 0) WHERE id = {:id}").
		Bind(dbx.Params{"count": count, "id": quota.Id}).
		Execute()
	return err
}

// IncrementDeviceCount atomically increases the device counter.
func IncrementDeviceCount(app core.App, userId string) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	_, err = app.DB().
		NewQuery("UPDATE user_quotas SET devices_registered = devices_registered + 1 WHERE id = {:id}").
		Bind(dbx.Params{"id": quota.Id}).
		Execute()
	return err
}

// DecrementDeviceCount atomically decreases the device counter.
func DecrementDeviceCount(app core.App, userId string) error {
	quota, err := getOrCreateQuota(app, userId)
	if err != nil {
		return err
	}

	_, err = app.DB().
		NewQuery("UPDATE user_quotas SET devices_registered = MAX(devices_registered - 1, 0) WHERE id = {:id}").
		Bind(dbx.Params{"id": quota.Id}).
		Execute()
	return err
}

// CreateDefaultQuota creates a quota record for a new user with the free plan.
func CreateDefaultQuota(app core.App, userId string) error {
	_, err := getOrCreateQuota(app, userId)
	return err
}

// ResetMonthlyQuotas resets SMS counters for users whose 30-day cycle has elapsed.
func ResetMonthlyQuotas(app core.App) error {
	const pageSize = 500
	now := time.Now().UTC()

	resetCount := 0
	for offset := 0; ; offset += pageSize {
		records, err := app.FindRecordsByFilter("user_quotas", "1=1", "", pageSize, offset)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}

		for _, q := range records {
			lastReset := q.GetDateTime("last_reset_date")
			if lastReset.IsZero() {
				continue
			}
			if now.Before(lastReset.Time().AddDate(0, 0, 30)) {
				continue // cycle hasn't elapsed yet
			}
			q.Set("sms_sent_this_month", 0)
			q.Set("last_reset_date", types.NowDateTime())
			if err := app.Save(q); err != nil {
				app.Logger().Warn("failed to reset quota",
					slog.String("quota", q.Id), slog.String("user", q.GetString("user")), slog.Any("error", err))
				continue
			}
			resetCount++
		}

		if len(records) < pageSize {
			break
		}
	}

	app.Logger().Info("Reset monthly quotas", slog.Int("count", resetCount))
	return nil
}

// getOrCreateQuota finds or creates a quota record for the user.
func getOrCreateQuota(app core.App, userId string) (*core.Record, error) {
	record, err := app.FindFirstRecordByFilter(
		"user_quotas",
		"user = {:userId}",
		dbx.Params{"userId": userId},
	)
	if err == nil && record != nil {
		return record, nil
	}

	// Find or create free plan
	freePlan, err := findFreePlan(app)
	if err != nil {
		return nil, err
	}

	collection, err := app.FindCollectionByNameOrId("user_quotas")
	if err != nil {
		return nil, err
	}

	quota := core.NewRecord(collection)
	quota.Set("user", userId)
	quota.Set("plan", freePlan.Id)
	quota.Set("sms_sent_this_month", 0)
	quota.Set("devices_registered", 0)
	quota.Set("last_reset_date", types.NowDateTime())

	if err := app.Save(quota); err != nil {
		// Unique constraint race: another request created it first — re-fetch
		// (same pattern as GetOrCreateBalance).
		if existing, retryErr := app.FindFirstRecordByFilter(
			"user_quotas",
			"user = {:userId}",
			dbx.Params{"userId": userId},
		); retryErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}

	return quota, nil
}

func countActiveScheduledSMS(app core.App, userId string) (int, error) {
	var count int
	err := app.DB().
		NewQuery("SELECT COUNT(*) FROM scheduled_sms WHERE user = {:userId} AND status IN ('active', 'paused')").
		Bind(dbx.Params{"userId": userId}).
		Row(&count)
	return count, err
}

func countIntegrations(app core.App, userId string) (int, error) {
	var count int
	err := app.DB().
		NewQuery("SELECT COUNT(*) FROM webhook_configs WHERE user = {:userId}").
		Bind(dbx.Params{"userId": userId}).
		Row(&count)
	return count, err
}

func findFreePlan(app core.App) (*core.Record, error) {
	record, err := app.FindFirstRecordByFilter(
		"user_plans",
		"name ~ 'free'",
	)
	if err == nil && record != nil {
		return record, nil
	}

	// Create free plan if it doesn't exist
	collection, err := app.FindCollectionByNameOrId("user_plans")
	if err != nil {
		return nil, err
	}

	plan := core.NewRecord(collection)
	plan.Set("name", "Free")
	plan.Set("max_sms_per_month", 50)
	plan.Set("max_devices", 1)
	plan.Set("max_scheduled_sms", 1)
	plan.Set("max_integrations", 1)
	plan.Set("price", 0)
	plan.Set("price_yearly", 0)
	plan.Set("is_public", true)

	if err := app.Save(plan); err != nil {
		return nil, err
	}

	return plan, nil
}
