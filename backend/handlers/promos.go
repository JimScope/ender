package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vendel/services"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterPromoRoutes registers promo code endpoints.
func RegisterPromoRoutes(se *core.ServeEvent) {
	// POST /api/promos/redeem — Redeem a promo code
	se.Router.POST("/api/promos/redeem", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		var body struct {
			Code string `json:"code"`
		}
		if err := e.BindBody(&body); err != nil {
			return apis.NewBadRequestError("Invalid request body", nil)
		}

		code := strings.TrimSpace(strings.ToUpper(body.Code))
		if code == "" {
			return apis.NewBadRequestError("Code is required", nil)
		}

		// Find the promo code
		promo, err := e.App.FindFirstRecordByFilter(
			"promo_codes",
			"code = {:code}",
			dbx.Params{"code": code},
		)
		if err != nil {
			return apis.NewApiError(http.StatusNotFound, "Invalid promo code", nil)
		}

		// Validate: active
		if !promo.GetBool("active") {
			return apis.NewApiError(http.StatusGone, "This promo code is no longer active", nil)
		}

		// Validate: not expired
		expiresAt := promo.GetDateTime("expires_at")
		if !expiresAt.IsZero() && expiresAt.Time().Before(time.Now().UTC()) {
			return apis.NewApiError(http.StatusGone, "This promo code has expired", nil)
		}

		// Validate: max redemptions not reached (re-checked inside the tx)
		maxRedemptions := promo.GetInt("max_redemptions")
		if maxRedemptions > 0 && promo.GetInt("times_redeemed") >= maxRedemptions {
			return apis.NewApiError(http.StatusGone, "This promo code has been fully redeemed", nil)
		}

		amount := promo.GetFloat("amount")

		// Check + credit + redemption record + counter share one transaction:
		// PocketBase serializes write transactions, so concurrent redeems of
		// the same code can neither double-credit one user nor exceed
		// max_redemptions. A failure anywhere rolls everything back.
		var newBalance float64
		err = e.App.RunInTransaction(func(txApp core.App) error {
			// Re-read the promo inside the tx so the counter check is current
			txPromo, err := txApp.FindRecordById("promo_codes", promo.Id)
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Internal error", nil)
			}
			timesRedeemed := txPromo.GetInt("times_redeemed")
			if maxRedemptions > 0 && timesRedeemed >= maxRedemptions {
				return apis.NewApiError(http.StatusGone, "This promo code has been fully redeemed", nil)
			}

			// Validate: user hasn't already redeemed this code
			existing, _ := txApp.FindFirstRecordByFilter(
				"promo_redemptions",
				"user = {:userId} && promo_code = {:promoId}",
				dbx.Params{"userId": userId, "promoId": promo.Id},
			)
			if existing != nil {
				return apis.NewApiError(http.StatusConflict, "You have already redeemed this code", nil)
			}

			newBalance, err = services.CreditBalance(txApp, userId, amount)
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Failed to credit balance", nil)
			}

			redemptionCol, err := txApp.FindCollectionByNameOrId("promo_redemptions")
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Internal error", nil)
			}
			redemption := core.NewRecord(redemptionCol)
			redemption.Set("user", userId)
			redemption.Set("promo_code", promo.Id)
			redemption.Set("amount_credited", amount)
			if err := txApp.Save(redemption); err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Failed to record redemption", nil)
			}

			txPromo.Set("times_redeemed", timesRedeemed+1)
			return txApp.Save(txPromo)
		})
		if err != nil {
			return err
		}

		e.App.Logger().Info("promo code redeemed",
			slog.String("user", userId),
			slog.String("code", code),
			slog.Float64("amount", amount),
		)

		return e.JSON(http.StatusOK, map[string]any{
			"message":     fmt.Sprintf("$%.2f credited to your balance", amount),
			"amount":      amount,
			"new_balance": newBalance,
		})
	}).Bind(apis.RequireAuth("users"))
}
