package handlers

import (
	"vendel/middleware"
	"vendel/services"
	"vendel/services/payment"
	"fmt"
	"net/http"
	"os"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterPlanRoutes registers plan/subscription-related routes.
func RegisterPlanRoutes(se *core.ServeEvent) {
	// GET /api/plans/quota — Get user quota info
	se.Router.GET("/api/plans/quota", func(e *core.RequestEvent) error {
		userId, err := middleware.ResolveAuthOrAPIKey(e)
		if err != nil {
			return apis.NewUnauthorizedError("Authentication required", nil)
		}

		quota, err := services.GetUserQuota(e.App, userId)
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, err.Error(), nil)
		}

		return e.JSON(http.StatusOK, quota)
	})

	// PUT /api/plans/upgrade — Activate subscription (balance-only)
	se.Router.PUT("/api/plans/upgrade", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		var body struct {
			PlanID       string `json:"plan_id"`
			BillingCycle string `json:"billing_cycle"`
		}
		if err := e.BindBody(&body); err != nil {
			return apis.NewBadRequestError("Invalid request body", nil)
		}

		if body.BillingCycle == "" {
			body.BillingCycle = "monthly"
		}

		sub, err := services.StartSubscription(e.App, userId, body.PlanID, body.BillingCycle)
		if err != nil {
			return handleServiceError(e, err)
		}

		return e.JSON(http.StatusOK, map[string]any{
			"subscription_id": sub.Id,
			"status":          sub.GetString("status"),
			"message":         "Subscription activated",
		})
	}).Bind(apis.RequireAuth("users"))

	// POST /api/plans/topup — Add funds via a payment provider
	se.Router.POST("/api/plans/topup", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		var body struct {
			Amount   float64 `json:"amount"`
			Provider string  `json:"provider"`
		}
		if err := e.BindBody(&body); err != nil {
			return apis.NewBadRequestError("Invalid request body", nil)
		}

		if body.Provider == "" {
			return apis.NewBadRequestError("Provider is required", nil)
		}

		// Resolve provider with system_config fallback
		resolve := func(key string) string { return services.GetSystemConfigValue(e.App, key) }
		provider := payment.GetProviderWithConfig(body.Provider, resolve)
		if provider == nil {
			return apis.NewBadRequestError("Unknown or unconfigured provider: "+body.Provider, nil)
		}

		// For TronDealer, amount is optional (any deposit works)
		// For QvaPay/Stripe, amount is required with min/max bounds
		if provider.Name() != "trondealer" {
			if body.Amount < 1 {
				return apis.NewBadRequestError("Minimum top-up amount is $1.00", nil)
			}
			if body.Amount > 500 {
				return apis.NewBadRequestError("Maximum top-up amount is $500.00", nil)
			}
		}

		// Build callback URLs
		baseURL := os.Getenv("APP_URL")
		if baseURL == "" {
			baseURL = "http://localhost:8090"
		}
		webhookURL := fmt.Sprintf("%s/api/webhooks/%s", baseURL, provider.Name())

		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			frontendURL = baseURL
		}

		result, err := provider.CreateInvoice(payment.InvoiceRequest{
			Amount:      body.Amount,
			Currency:    "USD",
			Description: "Balance top-up",
			RemoteID:    userId,
			WebhookURL:  webhookURL,
			SuccessURL:  frontendURL + "/settings?topup=success",
			ErrorURL:    frontendURL + "/settings?topup=canceled",
		})
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "Provider error: "+err.Error(), nil)
		}

		// For TronDealer, the deposit webhook matches users by wallet address,
		// so the address we display MUST be the one persisted. Reuse the
		// registered wallet on subsequent top-ups; persist the new one (with
		// error handling) only on first assignment.
		if provider.Name() == "trondealer" {
			bal, err := services.GetOrCreateBalance(e.App, userId)
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "Failed to load balance record", nil)
			}

			walletAddress := bal.GetString("wallet_address")
			if walletAddress == "" {
				walletAddress = result.PaymentURL
				if err := services.SetWalletInfo(e.App, userId, walletAddress, result.InvoiceID); err != nil {
					// Showing an unregistered wallet would send deposits into
					// the void — fail loudly instead.
					return apis.NewApiError(http.StatusInternalServerError, "Failed to register deposit wallet", nil)
				}
			}

			return e.JSON(http.StatusOK, map[string]any{
				"provider":       provider.Name(),
				"wallet_address": walletAddress,
			})
		}

		return e.JSON(http.StatusOK, map[string]any{
			"provider":    provider.Name(),
			"payment_url": result.PaymentURL,
		})
	}).Bind(apis.RequireAuth("users"))

	// GET /api/plans/balance — Get user balance and wallet info
	se.Router.GET("/api/plans/balance", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		bal, err := services.GetOrCreateBalance(e.App, userId)
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, err.Error(), nil)
		}

		return e.JSON(http.StatusOK, map[string]any{
			"balance":        bal.GetFloat("balance"),
			"currency":       bal.GetString("currency"),
			"wallet_address": bal.GetString("wallet_address"),
			"wallet_id":      bal.GetString("wallet_id"),
		})
	}).Bind(apis.RequireAuth("users"))

	// POST /api/plans/cancel — Cancel subscription
	se.Router.POST("/api/plans/cancel", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		var body struct {
			Immediate bool `json:"immediate"`
		}
		_ = e.BindBody(&body)

		sub, err := services.CancelSubscription(e.App, userId, body.Immediate)
		if err != nil {
			return apis.NewBadRequestError(err.Error(), nil)
		}

		msg := "Subscription will cancel at period end"
		if body.Immediate {
			msg = "Subscription canceled immediately"
		}

		return e.JSON(http.StatusOK, map[string]any{
			"message":         msg,
			"subscription_id": sub.Id,
			"status":          sub.GetString("status"),
		})
	}).Bind(apis.RequireAuth("users"))
}
