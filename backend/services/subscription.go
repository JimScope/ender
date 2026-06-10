package services

import (
	"fmt"
	"log/slog"
	"net/mail"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/mailer"
	"github.com/pocketbase/pocketbase/tools/types"
)

// StartSubscription activates a subscription by debiting the user's balance.
// Subscriptions are balance-only — providers are used separately for top-ups.
func StartSubscription(
	app core.App,
	userId, planId, billingCycle string,
) (*core.Record, error) {
	if billingCycle != "monthly" && billingCycle != "yearly" {
		return nil, fmt.Errorf("invalid billing cycle %q: must be 'monthly' or 'yearly'", billingCycle)
	}

	existing, _ := findSubscriptionByUser(app, userId)
	if existing != nil {
		switch existing.GetString("status") {
		case "pending":
			_ = app.Delete(existing)
			existing = nil
		case "active":
			if existing.GetString("plan") == planId {
				return nil, fmt.Errorf("already subscribed to this plan")
			}
			return nil, fmt.Errorf("cancel current subscription before changing plans")
		}
	}

	plan, err := app.FindRecordById("user_plans", planId)
	if err != nil {
		return nil, fmt.Errorf("plan not found")
	}
	if !plan.GetBool("is_public") {
		return nil, fmt.Errorf("plan not available")
	}

	amount, periodDays := calculateBilling(plan, billingCycle)

	now := time.Now().UTC()
	periodEnd := now.Add(time.Duration(periodDays) * 24 * time.Hour)

	// Free plan — activate immediately (replacing any expired/canceled subscription)
	if amount <= 0 {
		if existing != nil {
			_ = app.Delete(existing)
		}
		sub, err := createSubscriptionRecord(app, userId, planId, billingCycle, "active", now, periodEnd)
		if err != nil {
			return nil, err
		}
		if err := updateUserQuota(app, userId, planId); err != nil {
			app.Logger().Warn("failed to update quota", slog.Any("error", err))
		}
		return sub, nil
	}

	// Delete existing expired/canceled subscription
	if existing != nil {
		_ = app.Delete(existing)
	}

	// Check balance
	bal, err := GetOrCreateBalance(app, userId)
	if err != nil {
		return nil, err
	}

	if bal.GetFloat("balance") < amount {
		return nil, fmt.Errorf("insufficient balance: need $%.2f, have $%.2f", amount, bal.GetFloat("balance"))
	}

	// Create subscription and activate by debiting balance
	sub, err := createSubscriptionRecord(app, userId, planId, billingCycle, "pending", now, now)
	if err != nil {
		return nil, err
	}

	if err := activateBalanceSubscription(app, sub, plan, userId, amount, periodDays); err != nil {
		return nil, err
	}

	return sub, nil
}

// CancelSubscription cancels a subscription, optionally immediately.
func CancelSubscription(app core.App, userId string, immediate bool) (*core.Record, error) {
	sub, err := findSubscriptionByUser(app, userId)
	if err != nil || sub == nil {
		return nil, fmt.Errorf("subscription not found")
	}

	if sub.GetString("status") == "canceled" {
		return nil, fmt.Errorf("already canceled")
	}

	now := types.NowDateTime()

	if immediate {
		sub.Set("status", "canceled")
		sub.Set("canceled_at", now)
		if err := app.Save(sub); err != nil {
			return nil, err
		}
		_ = downgradeToFreePlan(app, userId)
		return sub, nil
	}

	sub.Set("cancel_at_period_end", true)
	sub.Set("canceled_at", now)
	if err := app.Save(sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// CheckRenewals processes balance-based renewals for all active subscriptions
// due, finalizes end-of-period cancellations, and downgrades subscriptions
// stuck in past_due beyond the grace period.
func CheckRenewals(app core.App) error {
	now := FilterNow()

	subs, err := app.FindRecordsByFilter(
		"subscriptions",
		"status = 'active' && payment_method = 'balance' && current_period_end <= {:now} && cancel_at_period_end = false",
		"", 0, 0,
		dbx.Params{"now": now},
	)
	if err != nil {
		return err
	}

	for _, sub := range subs {
		if err := ProcessBalanceRenewal(app, sub.Id); err != nil {
			app.Logger().Warn("renewal failed", slog.String("subscription", sub.Id), slog.Any("error", err))
		}
	}

	finalizePeriodEndCancellations(app, now)
	downgradeExpiredPastDue(app)

	app.Logger().Info("Processed subscription renewals", slog.Int("count", len(subs)))
	return nil
}

// finalizePeriodEndCancellations cancels subscriptions whose period elapsed
// with cancel_at_period_end set, downgrading the user to the free plan.
func finalizePeriodEndCancellations(app core.App, now string) {
	subs, err := app.FindRecordsByFilter(
		"subscriptions",
		"status = 'active' && cancel_at_period_end = true && current_period_end <= {:now}",
		"", 0, 0,
		dbx.Params{"now": now},
	)
	if err != nil {
		app.Logger().Warn("failed to query period-end cancellations", slog.Any("error", err))
		return
	}

	for _, sub := range subs {
		sub.Set("status", "canceled")
		if err := app.Save(sub); err != nil {
			app.Logger().Warn("failed to cancel subscription at period end",
				slog.String("subscription", sub.Id), slog.Any("error", err))
			continue
		}
		if err := downgradeToFreePlan(app, sub.GetString("user")); err != nil {
			app.Logger().Warn("failed to downgrade after period-end cancellation",
				slog.String("subscription", sub.Id), slog.Any("error", err))
		}
	}
}

// pastDueGracePeriod is how long a subscription may remain past_due (counted
// from the missed current_period_end) before the user is downgraded to the
// free plan. Within the grace period the subscription reactivates
// automatically when the user tops up enough balance (see creditAndActivate).
const pastDueGracePeriod = 7 * 24 * time.Hour

// downgradeExpiredPastDue cancels past_due subscriptions whose grace period
// has elapsed without payment, downgrading the user to the free plan.
func downgradeExpiredPastDue(app core.App) {
	cutoff := FilterTime(time.Now().UTC().Add(-pastDueGracePeriod))
	subs, err := app.FindRecordsByFilter(
		"subscriptions",
		"status = 'past_due' && current_period_end <= {:cutoff}",
		"", 0, 0,
		dbx.Params{"cutoff": cutoff},
	)
	if err != nil {
		app.Logger().Warn("failed to query expired past_due subscriptions", slog.Any("error", err))
		return
	}

	for _, sub := range subs {
		sub.Set("status", "canceled")
		if err := app.Save(sub); err != nil {
			app.Logger().Warn("failed to cancel expired past_due subscription",
				slog.String("subscription", sub.Id), slog.Any("error", err))
			continue
		}
		if err := downgradeToFreePlan(app, sub.GetString("user")); err != nil {
			app.Logger().Warn("failed to downgrade expired past_due subscription",
				slog.String("subscription", sub.Id), slog.Any("error", err))
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────────────

func calculateBilling(plan *core.Record, cycle string) (amount float64, periodDays int) {
	if cycle == "monthly" {
		return plan.GetFloat("price"), 30
	}
	amount = plan.GetFloat("price_yearly")
	if amount <= 0 {
		amount = plan.GetFloat("price") * 12
	}
	return amount, 365
}

func findSubscriptionByUser(app core.App, userId string) (*core.Record, error) {
	return app.FindFirstRecordByFilter(
		"subscriptions",
		"user = {:userId}",
		dbx.Params{"userId": userId},
	)
}

func createSubscriptionRecord(
	app core.App,
	userId, planId, billingCycle, status string,
	periodStart, periodEnd time.Time,
) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("subscriptions")
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("user", userId)
	record.Set("plan", planId)
	record.Set("billing_cycle", billingCycle)
	record.Set("payment_method", "balance")
	record.Set("provider", "")
	record.Set("status", status)
	record.Set("current_period_start", periodStart)
	record.Set("current_period_end", periodEnd)
	record.Set("cancel_at_period_end", false)

	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

func createPaymentRecord(
	app core.App,
	subscriptionId string, amount float64, currency, provider string,
	periodStart, periodEnd time.Time, status string,
) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("payments")
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("subscription", subscriptionId)
	record.Set("amount", amount)
	record.Set("currency", currency)
	record.Set("provider", provider)
	record.Set("status", status)
	record.Set("period_start", periodStart)
	record.Set("period_end", periodEnd)

	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

func updateUserQuota(app core.App, userId, planId string) error {
	quota, err := app.FindFirstRecordByFilter(
		"user_quotas",
		"user = {:userId}",
		dbx.Params{"userId": userId},
	)
	if err != nil || quota == nil {
		return nil
	}
	quota.Set("plan", planId)
	quota.Set("sms_sent_this_month", 0)
	quota.Set("last_reset_date", types.NowDateTime())
	return app.Save(quota)
}

func downgradeToFreePlan(app core.App, userId string) error {
	plan, err := findFreePlan(app)
	if err != nil {
		return err
	}
	return updateUserQuota(app, userId, plan.Id)
}

func notifyRenewalFailure(app core.App, sub, plan *core.Record, chargeErr error) {
	user, err := app.FindRecordById("users", sub.GetString("user"))
	if err != nil {
		app.Logger().Warn("could not find user for renewal failure email", slog.Any("error", err))
		return
	}

	email := user.GetString("email")
	if email == "" {
		return
	}

	planName := plan.GetString("name")
	msg := &mailer.Message{
		From: mail.Address{
			Address: app.Settings().Meta.SenderAddress,
			Name:    app.Settings().Meta.SenderName,
		},
		To:      []mail.Address{{Address: email}},
		Subject: "Subscription renewal failed",
		HTML: fmt.Sprintf(
			`<p>Hi,</p>
<p>We were unable to renew your <strong>%s</strong> subscription due to insufficient balance.</p>
<p>Please add funds to avoid service interruption.</p>`,
			planName,
		),
	}

	if err := app.NewMailClient().Send(msg); err != nil {
		app.Logger().Warn("failed to send renewal failure email", slog.String("email", email), slog.Any("error", err))
	}
}
