package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"vendel/middleware"
	"vendel/services"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

var e164Regex = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)

// RegisterSMSRoutes registers custom SMS API routes.
func RegisterSMSRoutes(se *core.ServeEvent) {
	se.Router.POST("/api/sms/send", handleSendSMS)
	se.Router.POST("/api/sms/send-template", handleSendSMSTemplate)
	se.Router.POST("/api/sms/report", handleSMSReport)
	se.Router.POST("/api/sms/incoming", handleIncomingSMS)
	se.Router.GET("/api/sms/pending", handlePendingMessages)
	se.Router.POST("/api/sms/fcm-token", handleFCMToken)
	se.Router.GET("/api/sms/status/{id}", handleMessageStatus)
	se.Router.GET("/api/sms/batch/{batchId}", handleBatchStatus)
	se.Router.GET("/api/sms/messages", handleListMessages)
}

// handleSendSMS sends an SMS with a direct body (auth: JWT or API key).
func handleSendSMS(e *core.RequestEvent) error {
	userId, err := middleware.ResolveAuthOrAPIKey(e)
	if err != nil {
		return apis.NewUnauthorizedError("Authentication required", nil)
	}

	var body struct {
		Recipients []string `json:"recipients"`
		Body       string   `json:"body"`
		DeviceID   string   `json:"device_id"`
		GroupIDs   []string `json:"group_ids"`
	}
	if err := e.BindBody(&body); err != nil {
		return apis.NewBadRequestError("Invalid request body", nil)
	}

	recipients, err := resolveRecipients(e.App, userId, body.Recipients, body.GroupIDs)
	if err != nil {
		return apis.NewBadRequestError(err.Error(), nil)
	}

	if body.Body == "" {
		return apis.NewBadRequestError("Message body required", nil)
	}
	body.Body = services.StripInvisibleUnicode(body.Body)
	if len(body.Body) > services.MaxMessageBodyLength {
		return apis.NewBadRequestError(
			fmt.Sprintf("Message body exceeds %d character limit", services.MaxMessageBodyLength), nil)
	}

	messages, err := services.SendSMS(e.App, userId, recipients, body.Body, body.DeviceID, nil)
	if err != nil {
		return handleServiceError(e, err)
	}

	return e.JSON(http.StatusOK, buildSendResponse(messages, len(recipients)))
}

// handleSendSMSTemplate sends an SMS using a saved template (auth: JWT or API key).
func handleSendSMSTemplate(e *core.RequestEvent) error {
	userId, err := middleware.ResolveAuthOrAPIKey(e)
	if err != nil {
		return apis.NewUnauthorizedError("Authentication required", nil)
	}

	var body struct {
		Recipients []string          `json:"recipients"`
		TemplateID string            `json:"template_id"`
		Variables  map[string]string `json:"variables"`
		DeviceID   string            `json:"device_id"`
		GroupIDs   []string          `json:"group_ids"`
	}
	if err := e.BindBody(&body); err != nil {
		return apis.NewBadRequestError("Invalid request body", nil)
	}

	recipients, err := resolveRecipients(e.App, userId, body.Recipients, body.GroupIDs)
	if err != nil {
		return apis.NewBadRequestError(err.Error(), nil)
	}

	tmpl, err := resolveTemplate(e.App, userId, body.TemplateID, body.Variables)
	if err != nil {
		return apis.NewBadRequestError(err.Error(), nil)
	}

	messages, err := services.SendSMS(e.App, userId, recipients, "", body.DeviceID, tmpl)
	if err != nil {
		return handleServiceError(e, err)
	}

	return e.JSON(http.StatusOK, buildSendResponse(messages, len(recipients)))
}

// resolveTemplate validates ownership and builds TemplateOptions from a template ID.
func resolveTemplate(app core.App, userId, templateId string, variables map[string]string) (*services.TemplateOptions, error) {
	if templateId == "" {
		return nil, fmt.Errorf("template_id required")
	}

	record, err := app.FindRecordById("sms_templates", templateId)
	if err != nil {
		return nil, fmt.Errorf("template not found")
	}
	if record.GetString("user") != userId {
		return nil, fmt.Errorf("template does not belong to user")
	}

	templateBody := services.GetRecordBody(record)

	vars := services.ExtractVariables(templateBody)
	_, custom := services.ClassifyVariables(vars)
	for _, v := range custom {
		if _, ok := variables[v]; !ok {
			return nil, fmt.Errorf("missing variable: %s", v)
		}
	}

	return &services.TemplateOptions{
		TemplateBody: templateBody,
		Variables:    variables,
	}, nil
}

// handleSMSReport processes a device ACK callback (auth: device API key).
func handleSMSReport(e *core.RequestEvent) error {
	device, err := middleware.AuthenticateDevice(e)
	if err != nil {
		return apis.NewUnauthorizedError("Invalid API key", nil)
	}

	var body struct {
		MessageID    string `json:"message_id"`
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
	}
	if err := e.BindBody(&body); err != nil {
		return apis.NewBadRequestError("Invalid request body", nil)
	}

	validStatuses := map[string]bool{"sent": true, "delivered": true, "failed": true}
	if !validStatuses[body.Status] {
		return apis.NewBadRequestError("Invalid status: must be sent, delivered, or failed", nil)
	}

	if err := services.ProcessSMSAck(e.App, device.Id, body.MessageID, body.Status, body.ErrorMessage); err != nil {
		return apis.NewNotFoundError(err.Error(), nil)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"success":    true,
		"message_id": body.MessageID,
		"status":     body.Status,
	})
}

// handleIncomingSMS processes an incoming SMS from a device (auth: device API key).
func handleIncomingSMS(e *core.RequestEvent) error {
	device, err := middleware.AuthenticateDevice(e)
	if err != nil {
		return apis.NewUnauthorizedError("Invalid API key", nil)
	}

	var body struct {
		FromNumber string `json:"from_number"`
		Body       string `json:"body"`
		Timestamp  string `json:"timestamp"`
	}
	if err := e.BindBody(&body); err != nil {
		return apis.NewBadRequestError("Invalid request body", nil)
	}

	userId := device.GetString("user")
	message, err := services.HandleIncomingSMS(e.App, userId, device.Id, body.FromNumber, body.Body, body.Timestamp)
	if err != nil {
		return apis.NewApiError(http.StatusInternalServerError, err.Error(), nil)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"success":    true,
		"message_id": message.Id,
	})
}

// handlePendingMessages returns pending messages for a device (auth: device API key).
func handlePendingMessages(e *core.RequestEvent) error {
	device, err := middleware.AuthenticateDevice(e)
	if err != nil {
		return apis.NewUnauthorizedError("Invalid API key", nil)
	}

	claimed, err := services.ClaimPendingMessages(e.App, device.Id)
	if err != nil {
		return apis.NewApiError(http.StatusInternalServerError, "Failed to claim messages", nil)
	}

	type pendingMsg struct {
		MessageID string `json:"message_id"`
		Recipient string `json:"recipient"`
		Body      string `json:"body"`
	}
	msgs := make([]pendingMsg, 0, len(claimed))
	for _, r := range claimed {
		msgs = append(msgs, pendingMsg{
			MessageID: r.Id,
			Recipient: r.GetString("to"),
			Body:      services.GetRecordBody(r),
		})
	}

	return e.JSON(http.StatusOK, map[string]any{
		"device_id": device.Id,
		"messages":  msgs,
	})
}

// handleFCMToken updates a device's FCM token (auth: device API key).
func handleFCMToken(e *core.RequestEvent) error {
	device, err := middleware.AuthenticateDevice(e)
	if err != nil {
		return apis.NewUnauthorizedError("Invalid API key", nil)
	}

	var body struct {
		FCMToken string `json:"fcm_token"`
	}
	if err := e.BindBody(&body); err != nil {
		return apis.NewBadRequestError("Invalid request body", nil)
	}

	device.Set("fcm_token", body.FCMToken)
	if err := e.App.Save(device); err != nil {
		return apis.NewApiError(http.StatusInternalServerError, "Failed to update token", nil)
	}

	return e.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleMessageStatus returns the status of a single SMS message (auth: JWT or API key).
func handleMessageStatus(e *core.RequestEvent) error {
	userId, err := middleware.ResolveAuthOrAPIKey(e)
	if err != nil {
		return apis.NewUnauthorizedError("Authentication required", nil)
	}

	messageId := e.Request.PathValue("id")
	record, err := e.App.FindFirstRecordByFilter(
		"sms_messages",
		"id = {:id} && user = {:userId}",
		dbx.Params{"id": messageId, "userId": userId},
	)
	if err != nil {
		return apis.NewNotFoundError("Message not found", nil)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"id":            record.Id,
		"batch_id":      record.GetString("batch_id"),
		"recipient":     record.GetString("to"),
		"status":        record.GetString("status"),
		"error_message": record.GetString("error_message"),
		"device_id":     record.GetString("device"),
		"created":       record.GetDateTime("created"),
		"updated":       record.GetDateTime("updated"),
	})
}

// handleBatchStatus returns the status of all messages in a batch (auth: JWT or API key).
func handleBatchStatus(e *core.RequestEvent) error {
	userId, err := middleware.ResolveAuthOrAPIKey(e)
	if err != nil {
		return apis.NewUnauthorizedError("Authentication required", nil)
	}

	batchId := e.Request.PathValue("batchId")
	messages, err := e.App.FindRecordsByFilter(
		"sms_messages",
		"batch_id = {:batchId} && user = {:userId}",
		"-created", 0, 0,
		dbx.Params{"batchId": batchId, "userId": userId},
	)
	if err != nil || len(messages) == 0 {
		return apis.NewNotFoundError("Batch not found", nil)
	}

	counts := map[string]int{}
	items := make([]map[string]any, len(messages))
	for i, m := range messages {
		status := m.GetString("status")
		counts[status]++
		items[i] = map[string]any{
			"id":            m.Id,
			"recipient":     m.GetString("to"),
			"status":        status,
			"error_message": m.GetString("error_message"),
			"created":       m.GetDateTime("created"),
			"updated":       m.GetDateTime("updated"),
		}
	}

	return e.JSON(http.StatusOK, map[string]any{
		"batch_id":   batchId,
		"total":      len(messages),
		"status_counts": counts,
		"messages":   items,
	})
}

// handleListMessages returns a paginated list of the user's SMS messages
// (auth: JWT or API key). Supports filtering by status, device, batch,
// recipient, and a created-at date range.
func handleListMessages(e *core.RequestEvent) error {
	userId, err := middleware.ResolveAuthOrAPIKey(e)
	if err != nil {
		return apis.NewUnauthorizedError("Authentication required", nil)
	}

	page, perPage := parsePagination(e)
	offset := (page - 1) * perPage

	filter := "user = {:userId}"
	params := dbx.Params{"userId": userId}
	// CountRecords takes raw SQL expressions, not PocketBase filter syntax —
	// build both in parallel so total_items matches the filtered listing.
	countExprs := []dbx.Expression{dbx.HashExp{"user": userId}}

	q := e.Request.URL.Query()

	if status := strings.TrimSpace(q.Get("status")); status != "" {
		filter += " && status = {:status}"
		params["status"] = status
		countExprs = append(countExprs, dbx.HashExp{"status": status})
	}

	if deviceID := strings.TrimSpace(q.Get("device_id")); deviceID != "" {
		filter += " && device = {:deviceId}"
		params["deviceId"] = deviceID
		countExprs = append(countExprs, dbx.HashExp{"device": deviceID})
	}

	if batchID := strings.TrimSpace(q.Get("batch_id")); batchID != "" {
		filter += " && batch_id = {:batchId}"
		params["batchId"] = batchID
		countExprs = append(countExprs, dbx.HashExp{"batch_id": batchID})
	}

	if recipient := strings.TrimSpace(q.Get("recipient")); recipient != "" {
		filter += " && to = {:recipient}"
		params["recipient"] = recipient
		countExprs = append(countExprs, dbx.HashExp{"to": recipient})
	}

	if from := strings.TrimSpace(q.Get("from")); from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return apis.NewBadRequestError("Invalid 'from' timestamp; expected ISO8601 (RFC3339)", nil)
		}
		fromDate := t.UTC().Format("2006-01-02 15:04:05.000Z")
		filter += " && created >= {:fromDate}"
		params["fromDate"] = fromDate
		countExprs = append(countExprs, dbx.NewExp("[[created]] >= {:fromDate}", dbx.Params{"fromDate": fromDate}))
	}

	if to := strings.TrimSpace(q.Get("to")); to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return apis.NewBadRequestError("Invalid 'to' timestamp; expected ISO8601 (RFC3339)", nil)
		}
		toDate := t.UTC().Format("2006-01-02 15:04:05.000Z")
		filter += " && created <= {:toDate}"
		params["toDate"] = toDate
		countExprs = append(countExprs, dbx.NewExp("[[created]] <= {:toDate}", dbx.Params{"toDate": toDate}))
	}

	records, err := e.App.FindRecordsByFilter(
		"sms_messages",
		filter,
		"-created",
		perPage,
		offset,
		params,
	)
	if err != nil {
		records = []*core.Record{}
	}

	totalItems, err := e.App.CountRecords("sms_messages", countExprs...)
	if err != nil {
		return apis.NewApiError(http.StatusInternalServerError, "Failed to count messages", nil)
	}
	totalPages := int(totalItems) / perPage
	if int(totalItems)%perPage != 0 {
		totalPages++
	}

	items := make([]map[string]any, 0, len(records))
	for _, r := range records {
		items = append(items, map[string]any{
			"id":            r.Id,
			"batch_id":      r.GetString("batch_id"),
			"recipient":     r.GetString("to"),
			"from_number":   r.GetString("from_number"),
			"body":          services.GetRecordBody(r),
			"status":        r.GetString("status"),
			"message_type":  r.GetString("message_type"),
			"error_message": r.GetString("error_message"),
			"device_id":     r.GetString("device"),
			"sent_at":       r.GetDateTime("sent_at"),
			"delivered_at":  r.GetDateTime("delivered_at"),
			"created":       r.GetDateTime("created"),
			"updated":       r.GetDateTime("updated"),
		})
	}

	return e.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"page":        page,
		"per_page":    perPage,
		"total_items": totalItems,
		"total_pages": totalPages,
	})
}

// resolveRecipients expands group contacts and validates E.164 format.
func resolveRecipients(app core.App, userId string, recipients []string, groupIDs []string) ([]string, error) {
	seen := make(map[string]bool, len(recipients))
	result := make([]string, 0, len(recipients))
	for _, r := range recipients {
		if !seen[r] {
			result = append(result, r)
			seen[r] = true
		}
	}

	for _, groupID := range groupIDs {
		groupContacts, _ := app.FindRecordsByFilter(
			"contacts",
			"user = {:userId} && groups.id ?= {:groupId}",
			"", 0, 0,
			dbx.Params{"userId": userId, "groupId": groupID},
		)
		for _, c := range groupContacts {
			phone := c.GetString("phone_number")
			if !seen[phone] {
				result = append(result, phone)
				seen[phone] = true
			}
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("at least one recipient required")
	}
	for _, r := range result {
		if !e164Regex.MatchString(r) {
			return nil, fmt.Errorf("invalid phone number: %s. Must be E.164 format (e.g. +1234567890)", r)
		}
	}
	return result, nil
}

// buildSendResponse creates the standard response for send endpoints.
func buildSendResponse(messages []*core.Record, recipientCount int) map[string]any {
	ids := make([]string, len(messages))
	for i, m := range messages {
		ids[i] = m.Id
	}

	var batchId string
	if len(messages) > 0 {
		batchId = messages[0].GetString("batch_id")
	}

	return map[string]any{
		"batch_id":         batchId,
		"message_ids":      ids,
		"recipients_count": recipientCount,
		"status":           "accepted",
	}
}
