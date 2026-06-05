package handlers

import (
	"encoding/json"
	"vendel/services"
	"vendel/services/payment"
	"io"
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterWebhookRoutes registers payment provider webhook endpoints.
func RegisterWebhookRoutes(se *core.ServeEvent) {
	handler := func(e *core.RequestEvent) error {
		providerName := e.Request.PathValue("provider")
		return handlePaymentWebhook(e, providerName)
	}
	// POST /api/webhooks/{provider} — Payment webhook callback.
	// The trailing-slash variant is registered too so a webhook URL copied
	// into a provider dashboard with a trailing slash still reaches this
	// handler instead of falling through to the GET-only SPA catch-all,
	// which would make Go's ServeMux reply 405 Method Not Allowed.
	se.Router.POST("/api/webhooks/{provider}", handler)
	se.Router.POST("/api/webhooks/{provider}/", handler)
}

// RegisterUserWebhookRoutes registers user-facing webhook management endpoints.
func RegisterUserWebhookRoutes(se *core.ServeEvent) {
	// POST /api/webhooks/test — Test a webhook endpoint (auth: JWT)
	se.Router.POST("/api/webhooks/test", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		var body struct {
			WebhookID string `json:"webhook_id"`
		}
		if err := e.BindBody(&body); err != nil || body.WebhookID == "" {
			return apis.NewBadRequestError("webhook_id is required", nil)
		}

		webhook, err := e.App.FindRecordById("webhook_configs", body.WebhookID)
		if err != nil {
			return apis.NewNotFoundError("Webhook not found", nil)
		}
		if webhook.GetString("user") != userId {
			return apis.NewForbiddenError("Not your webhook", nil)
		}

		result := services.SendTestWebhook(e.App, webhook)
		return e.JSON(http.StatusOK, result.ToJSON())
	}).Bind(apis.RequireAuth("users"))

	// POST /api/webhooks/retry — Retry a failed webhook delivery (auth: JWT)
	se.Router.POST("/api/webhooks/retry", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		var body struct {
			LogID string `json:"log_id"`
		}
		if err := e.BindBody(&body); err != nil || body.LogID == "" {
			return apis.NewBadRequestError("log_id is required", nil)
		}

		// Validate the log belongs to the user via its webhook config
		logRecord, err := e.App.FindRecordById("webhook_delivery_logs", body.LogID)
		if err != nil {
			return apis.NewNotFoundError("Delivery log not found", nil)
		}
		webhook, err := e.App.FindRecordById("webhook_configs", logRecord.GetString("webhook"))
		if err != nil {
			return apis.NewNotFoundError("Webhook not found", nil)
		}
		if webhook.GetString("user") != userId {
			return apis.NewForbiddenError("Not your webhook", nil)
		}

		result, err := services.RetryWebhookDelivery(e.App, body.LogID)
		if err != nil {
			return apis.NewBadRequestError(err.Error(), nil)
		}
		return e.JSON(http.StatusOK, result.ToJSON())
	}).Bind(apis.RequireAuth("users"))
}

func handlePaymentWebhook(e *core.RequestEvent, providerName string) error {
	// Read raw body for signature verification
	rawBody, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return apis.NewBadRequestError("Failed to read request body", nil)
	}

	// Parse JSON payload
	var payload map[string]any
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return apis.NewBadRequestError("Invalid JSON payload", nil)
	}

	// Extract headers
	headers := make(map[string]string)
	for key := range e.Request.Header {
		headers[key] = e.Request.Header.Get(key)
	}

	return processWebhookPayload(e, providerName, payment.WebhookRequest{
		RawBody: rawBody,
		Headers: headers,
		Payload: payload,
	})
}

func processWebhookPayload(e *core.RequestEvent, providerName string, webhookReq payment.WebhookRequest) error {
	provider := payment.GetProvider(providerName)
	if provider == nil {
		return apis.NewBadRequestError("Unknown payment provider", nil)
	}

	event, err := provider.ParseWebhook(webhookReq)
	if err != nil {
		e.App.Logger().Warn("webhook parse error", slog.String("provider", providerName), slog.Any("error", err))
		return apis.NewBadRequestError("Unrecognized webhook payload", nil)
	}

	// Idempotency: if the payment is already completed, return success without reprocessing
	if event.TransactionID != "" {
		existing, _ := services.FindPaymentByTransactionID(e.App, event.TransactionID)
		if existing != nil && existing.GetString("status") == "completed" {
			sub, _ := e.App.FindRecordById("subscriptions", existing.GetString("subscription"))
			subId := ""
			if sub != nil {
				subId = sub.Id
			}
			return e.JSON(http.StatusOK, map[string]any{
				"status":          "already_processed",
				"subscription_id": subId,
			})
		}
	}

	switch event.EventType {
	case payment.EventPaymentCompleted:
		// RemoteID = userId; credit balance and auto-activate pending subscription
		result, err := services.ProcessPaymentCredit(e.App, event.RemoteID, event.TransactionID, event.Amount)
		if err != nil {
			e.App.Logger().Error("ProcessPaymentCredit failed", slog.Any("error", err))
			return apis.NewApiError(http.StatusInternalServerError, "payment processing failed", nil)
		}
		return e.JSON(http.StatusOK, result)

	case payment.EventDepositReceived:
		// RemoteID = wallet address; look up user, credit balance, auto-activate
		result, err := services.ProcessDeposit(e.App, event.RemoteID, event.TransactionID, event.Amount, event.Asset)
		if err != nil {
			e.App.Logger().Error("ProcessDeposit failed", slog.Any("error", err))
			return apis.NewApiError(http.StatusInternalServerError, "deposit processing failed", nil)
		}
		return e.JSON(http.StatusOK, result)

	case payment.EventPaymentFailed:
		e.App.Logger().Warn("payment failed webhook", slog.String("remote_id", event.RemoteID))
		return e.JSON(http.StatusOK, map[string]string{"status": "noted"})

	default:
		return e.JSON(http.StatusOK, map[string]string{"status": "ignored"})
	}
}
