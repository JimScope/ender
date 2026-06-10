package services

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// GetOrCreateBalance returns the user's balance record, creating one if needed.
func GetOrCreateBalance(app core.App, userId string) (*core.Record, error) {
	record, err := app.FindFirstRecordByFilter(
		"user_balances",
		"user = {:userId}",
		dbx.Params{"userId": userId},
	)
	if err == nil {
		return record, nil
	}

	collection, err := app.FindCollectionByNameOrId("user_balances")
	if err != nil {
		return nil, fmt.Errorf("user_balances collection not found: %w", err)
	}

	record = core.NewRecord(collection)
	record.Set("user", userId)
	record.Set("balance", 0)
	record.Set("currency", "USDT")

	if err := app.Save(record); err != nil {
		// Unique constraint race: another request created it first — re-fetch
		if existing, retryErr := app.FindFirstRecordByFilter(
			"user_balances",
			"user = {:userId}",
			dbx.Params{"userId": userId},
		); retryErr == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to create balance record: %w", err)
	}
	return record, nil
}

// GetBalance returns the current balance for a user.
func GetBalance(app core.App, userId string) (float64, error) {
	record, err := GetOrCreateBalance(app, userId)
	if err != nil {
		return 0, err
	}
	return record.GetFloat("balance"), nil
}

// CreditBalance adds funds to a user's balance atomically.
// Returns the new balance.
func CreditBalance(app core.App, userId string, amount float64) (float64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("credit amount must be positive")
	}

	var newBalance float64
	err := app.RunInTransaction(func(txApp core.App) error {
		record, err := GetOrCreateBalance(txApp, userId)
		if err != nil {
			return err
		}

		newBalance = record.GetFloat("balance") + amount
		record.Set("balance", newBalance)
		return txApp.Save(record)
	})
	if err != nil {
		return 0, fmt.Errorf("failed to credit balance: %w", err)
	}

	app.Logger().Info("balance credited",
		slog.String("user", userId),
		slog.Float64("amount", amount),
		slog.Float64("new_balance", newBalance),
	)
	return newBalance, nil
}

// DebitBalance subtracts funds from a user's balance atomically.
// Returns the new balance. Fails if insufficient funds.
func DebitBalance(app core.App, userId string, amount float64) (float64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("debit amount must be positive")
	}

	var newBalance float64
	err := app.RunInTransaction(func(txApp core.App) error {
		record, err := GetOrCreateBalance(txApp, userId)
		if err != nil {
			return err
		}

		current := record.GetFloat("balance")
		if current < amount {
			return fmt.Errorf("insufficient balance: have %.2f, need %.2f", current, amount)
		}

		newBalance = current - amount
		record.Set("balance", newBalance)
		return txApp.Save(record)
	})
	if err != nil {
		return 0, err
	}

	app.Logger().Info("balance debited",
		slog.String("user", userId),
		slog.Float64("amount", amount),
		slog.Float64("new_balance", newBalance),
	)
	return newBalance, nil
}

// SetWalletInfo stores the TronDealer wallet address and ID on the user's balance record.
func SetWalletInfo(app core.App, userId, walletAddress, walletID string) error {
	record, err := GetOrCreateBalance(app, userId)
	if err != nil {
		return err
	}

	record.Set("wallet_address", walletAddress)
	record.Set("wallet_id", walletID)
	return app.Save(record)
}

// FindUserByWalletAddress looks up the userId by their assigned wallet address.
func FindUserByWalletAddress(app core.App, walletAddress string) (string, error) {
	record, err := app.FindFirstRecordByFilter(
		"user_balances",
		"wallet_address = {:addr}",
		dbx.Params{"addr": walletAddress},
	)
	if err != nil {
		return "", fmt.Errorf("no user found for wallet address %s", walletAddress)
	}
	return record.GetString("user"), nil
}

// ProcessDeposit handles a confirmed blockchain deposit (e.g. TronDealer):
// looks up user by wallet address, credits balance, and auto-activates pending subscriptions.
func ProcessDeposit(app core.App, walletAddress, txHash string, amount float64, asset string) (map[string]any, error) {
	userId, err := FindUserByWalletAddress(app, walletAddress)
	if err != nil {
		return nil, err
	}
	return creditAndActivate(app, userId, txHash, amount, asset)
}

// ProcessPaymentCredit handles a confirmed payment (e.g. QvaPay, Stripe):
// credits balance using the userId directly, and auto-activates pending subscriptions.
func ProcessPaymentCredit(app core.App, userId, txHash string, amount float64) (map[string]any, error) {
	return creditAndActivate(app, userId, txHash, amount, "USD")
}

// creditAndActivate is the shared logic for all deposit/payment flows:
// credit balance, then auto-activate a pending/past_due subscription if sufficient.
func creditAndActivate(app core.App, userId, txHash string, amount float64, asset string) (map[string]any, error) {
	var newBalance float64
	alreadyProcessed := false

	// Idempotency check + credit + payment marker share one transaction so
	// concurrent deliveries of the same provider webhook cannot double-credit.
	// PocketBase serializes write transactions, so the second delivery only
	// starts after the first commits and sees its payment marker.
	err := app.RunInTransaction(func(txApp core.App) error {
		if txHash != "" {
			existing, _ := FindPaymentByTransactionID(txApp, txHash)
			if existing != nil {
				alreadyProcessed = true
				return nil
			}
		}

		var creditErr error
		newBalance, creditErr = CreditBalance(txApp, userId, amount)
		if creditErr != nil {
			return creditErr
		}

		// Record the credit as a payment for idempotency tracking
		if txHash != "" {
			collection, err := txApp.FindCollectionByNameOrId("payments")
			if err != nil {
				return fmt.Errorf("payments collection not found: %w", err)
			}
			record := core.NewRecord(collection)
			record.Set("amount", amount)
			record.Set("currency", asset)
			record.Set("provider", "")
			record.Set("provider_transaction_id", txHash)
			record.Set("status", "completed")
			if err := txApp.Save(record); err != nil {
				return fmt.Errorf("failed to record payment marker: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if alreadyProcessed {
		return map[string]any{
			"status":  "already_processed",
			"tx_hash": txHash,
		}, nil
	}

	result := map[string]any{
		"status":      "balance_credited",
		"user":        userId,
		"amount":      amount,
		"asset":       asset,
		"tx_hash":     txHash,
		"new_balance": newBalance,
	}

	// Auto-activate pending or past_due subscription if balance is now sufficient
	sub, err := findSubscriptionByUser(app, userId)
	subStatus := ""
	if sub != nil {
		subStatus = sub.GetString("status")
	}
	if err == nil && sub != nil && (subStatus == "pending" || subStatus == "past_due") && sub.GetString("payment_method") == "balance" {
		plan, planErr := app.FindRecordById("user_plans", sub.GetString("plan"))
		if planErr == nil {
			planAmount, periodDays := calculateBilling(plan, sub.GetString("billing_cycle"))
			if newBalance >= planAmount {
				if activateErr := activateBalanceSubscription(app, sub, plan, userId, planAmount, periodDays); activateErr != nil {
					app.Logger().Warn("auto-activate after credit failed", slog.Any("error", activateErr))
				} else {
					result["subscription_activated"] = true
					result["subscription_id"] = sub.Id
				}
			}
		}
	}

	return result, nil
}

// activateBalanceSubscription debits the user's balance and activates the subscription.
func activateBalanceSubscription(app core.App, sub, plan *core.Record, userId string, amount float64, periodDays int) error {
	now := time.Now().UTC()
	periodEnd := now.Add(time.Duration(periodDays) * 24 * time.Hour)

	return app.RunInTransaction(func(txApp core.App) error {
		// Debit balance
		bal, err := GetOrCreateBalance(txApp, userId)
		if err != nil {
			return err
		}
		current := bal.GetFloat("balance")
		if current < amount {
			return fmt.Errorf("insufficient balance: have %.2f, need %.2f", current, amount)
		}
		bal.Set("balance", current-amount)
		if err := txApp.Save(bal); err != nil {
			return err
		}

		// Create payment record
		pay, err := createPaymentRecord(txApp, sub.Id, amount, "USD",
			sub.GetString("provider"), now, periodEnd, "completed")
		if err != nil {
			return err
		}
		pay.Set("paid_at", types.NowDateTime())
		if err := txApp.Save(pay); err != nil {
			return err
		}

		// Activate subscription
		sub.Set("status", "active")
		sub.Set("current_period_start", now)
		sub.Set("current_period_end", periodEnd)
		if err := txApp.Save(sub); err != nil {
			return err
		}

		return updateUserQuota(txApp, userId, sub.GetString("plan"))
	})
}

// ProcessBalanceRenewal handles renewal for a balance-based subscription.
// Called by CheckRenewals for subscriptions with payment_method = "balance".
func ProcessBalanceRenewal(app core.App, subscriptionId string) error {
	sub, err := app.FindRecordById("subscriptions", subscriptionId)
	if err != nil {
		return fmt.Errorf("subscription not found")
	}

	if sub.GetString("status") != "active" {
		return fmt.Errorf("subscription not active")
	}

	if sub.GetBool("cancel_at_period_end") {
		sub.Set("status", "canceled")
		if err := app.Save(sub); err != nil {
			return err
		}
		return downgradeToFreePlan(app, sub.GetString("user"))
	}

	userId := sub.GetString("user")
	plan, err := app.FindRecordById("user_plans", sub.GetString("plan"))
	if err != nil {
		return fmt.Errorf("plan not found")
	}

	amount, periodDays := calculateBilling(plan, sub.GetString("billing_cycle"))
	periodStart := sub.GetDateTime("current_period_end").Time()
	periodEnd := periodStart.Add(time.Duration(periodDays) * 24 * time.Hour)

	// Debit balance + create payment + renew subscription — all in one transaction
	err = app.RunInTransaction(func(txApp core.App) error {
		// Debit balance
		bal, balErr := GetOrCreateBalance(txApp, userId)
		if balErr != nil {
			return balErr
		}
		current := bal.GetFloat("balance")
		if current < amount {
			return fmt.Errorf("insufficient balance: have %.2f, need %.2f", current, amount)
		}
		bal.Set("balance", current-amount)
		if balErr = txApp.Save(bal); balErr != nil {
			return balErr
		}

		// Create payment record
		pay, payErr := createPaymentRecord(txApp, sub.Id, amount, "USD",
			sub.GetString("provider"), periodStart, periodEnd, "completed")
		if payErr != nil {
			return payErr
		}
		pay.Set("paid_at", types.NowDateTime())
		if payErr = txApp.Save(pay); payErr != nil {
			return payErr
		}

		// Renew subscription period
		sub.Set("current_period_start", periodStart)
		sub.Set("current_period_end", periodEnd)
		return txApp.Save(sub)
	})

	if err != nil {
		// Transaction failed — mark as past_due
		sub.Set("status", "past_due")
		_ = app.Save(sub)
		notifyRenewalFailure(app, sub, plan, err)
		return fmt.Errorf("balance renewal failed: %w", err)
	}

	return nil
}
