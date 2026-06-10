package services

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/routine"
	"github.com/pocketbase/pocketbase/tools/types"

	"vendel/services/smsprovider"
)

// TemplateOptions holds template interpolation data for per-recipient message generation.
type TemplateOptions struct {
	TemplateBody string
	Variables    map[string]string // custom variables (same for all recipients)
}

// SendSMS orchestrates the entire SMS sending process.
// If tmpl is non-nil, the message body is interpolated per recipient using template variables.
func SendSMS(app core.App, userId string, recipients []string, body string, deviceId string, tmpl *TemplateOptions) ([]*core.Record, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients provided")
	}

	// Reserve quota up front (atomic check-and-increment); release on failure.
	if err := ReserveSMSQuota(app, userId, len(recipients)); err != nil {
		return nil, err
	}

	aeumProvider := smsprovider.DefaultAEUM()
	devices, err := resolveDevices(app, userId, deviceId, aeumProvider)
	if err != nil {
		releaseQuota(app, userId, len(recipients))
		return nil, err
	}

	// Build contact lookup for template interpolation
	var contactMap map[string]*core.Record
	if tmpl != nil {
		contactMap = buildContactMap(app, userId, recipients)
	}

	messages, err := createMessageRecords(app, userId, recipients, body, devices, tmpl, contactMap)
	if err != nil {
		releaseQuota(app, userId, len(recipients))
		return nil, err
	}

	if len(devices) > 0 {
		dispatchOutgoing(app, messages)
	}

	return messages, nil
}

// releaseQuota refunds a failed reservation, logging instead of failing the
// caller (the user-facing error is the one that triggered the refund).
func releaseQuota(app core.App, userId string, count int) {
	if err := ReleaseSMSQuota(app, userId, count); err != nil {
		app.Logger().Warn("failed to release reserved SMS quota",
			slog.String("user", userId), slog.Int("count", count), slog.Any("error", err))
	}
}

// dispatchOutgoing fans out assigned messages to their transport: FCM tickle
// for physical devices (modems are notified via the realtime SSE hook on
// save) and provider dispatch for external providers (AEUM). Shared by the
// initial send and the retry cron.
func dispatchOutgoing(app core.App, messages []*core.Record) {
	physical, externalByType := partitionByProvider(app, messages)
	if len(physical) > 0 {
		routine.FireAndForget(func() { DispatchMessages(app, physical) })
	}
	for deviceType, msgs := range externalByType {
		provider := smsprovider.Get(deviceType)
		if provider == nil || len(msgs) == 0 {
			continue
		}
		routine.FireAndForget(func() { DispatchProviderMessages(app, provider, msgs) })
	}
}

// resolveDevices returns the target device(s) for sending. When deviceId is
// empty, physical devices (FCM/modem) take precedence over the global AEUM
// fallback so user-owned hardware keeps working without changes.
//
// NOTE (intentional design): the AEUM device is a managed, instance-wide
// service — it has no owner, so the per-user ownership check below does not
// apply to it. Any authenticated user may send through it; spending is
// bounded by the per-user SMS quota (ReserveSMSQuota), not by device ownership.
func resolveDevices(app core.App, userId, deviceId string, aeumProvider smsprovider.Provider) ([]*core.Record, error) {
	if deviceId != "" {
		device, err := app.FindRecordById("sms_devices", deviceId)
		if err != nil {
			return nil, fmt.Errorf("device not found: %w", err)
		}
		if device.GetString("device_type") == smsprovider.DeviceTypeAEUM {
			if aeumProvider == nil || !aeumProvider.IsConfigured() {
				return nil, fmt.Errorf("AWS End User Messaging is not configured")
			}
			return []*core.Record{device}, nil
		}
		if device.GetString("user") != userId {
			return nil, fmt.Errorf("device does not belong to user")
		}
		return []*core.Record{device}, nil
	}

	physical, err := app.FindRecordsByFilter(
		"sms_devices",
		"user = {:userId} && (fcm_token != '' || device_type = 'modem')",
		"-created",
		0, 0,
		dbx.Params{"userId": userId},
	)
	if err == nil && len(physical) > 0 {
		return physical, nil
	}

	if aeumProvider == nil || !aeumProvider.IsConfigured() {
		return nil, nil
	}
	aeum, err := app.FindFirstRecordByFilter("sms_devices", "device_type = {:t}", dbx.Params{"t": smsprovider.DeviceTypeAEUM})
	if err != nil || aeum == nil {
		return nil, nil
	}
	return []*core.Record{aeum}, nil
}

// buildContactMap fetches contacts matching the given phone numbers and indexes them by phone.
func buildContactMap(app core.App, userId string, phones []string) map[string]*core.Record {
	if len(phones) == 0 {
		return nil
	}

	contacts, err := app.FindRecordsByFilter(
		"contacts",
		"user = {:userId} && phone_number IN {:phones}",
		"", 0, 0,
		dbx.Params{"userId": userId, "phones": phones},
	)
	if err != nil || len(contacts) == 0 {
		return nil
	}

	m := make(map[string]*core.Record, len(contacts))
	for _, c := range contacts {
		m[c.GetString("phone_number")] = c
	}
	return m
}

// createMessageRecords creates sms_messages records, assigning devices via round-robin.
// When tmpl is non-nil, each message body is interpolated per recipient.
func createMessageRecords(
	app core.App,
	userId string,
	recipients []string,
	body string,
	devices []*core.Record,
	tmpl *TemplateOptions,
	contactMap map[string]*core.Record,
) ([]*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("sms_messages")
	if err != nil {
		return nil, fmt.Errorf("sms_messages collection not found: %w", err)
	}

	batchId := ""
	if len(recipients) > 1 {
		batchId = core.GenerateDefaultRandomId()
	}

	// Build and validate every record before persisting any of them. A
	// per-recipient failure (e.g. an over-length interpolated body) must abort
	// before the first Save, otherwise the caller refunds the full reservation
	// while the already-saved rows stay dispatch-eligible — undercounting quota
	// and orphaning real sends.
	messages := make([]*core.Record, 0, len(recipients))
	for i, recipient := range recipients {
		// Determine message body for this recipient
		msgBody := body
		if tmpl != nil {
			msgBody = interpolateForRecipient(tmpl, recipient, contactMap)
			if len(msgBody) > MaxMessageBodyLength {
				return nil, fmt.Errorf("interpolated message for %s exceeds %d character limit (%d chars)", recipient, MaxMessageBodyLength, len(msgBody))
			}
		}

		record := core.NewRecord(collection)
		record.Set("to", recipient)
		record.Set("body", msgBody)
		record.Set("user", userId)
		record.Set("message_type", "outgoing")
		record.Set("webhook_sent", false)

		if batchId != "" {
			record.Set("batch_id", batchId)
		}

		if len(devices) > 0 {
			device := devices[i%len(devices)]
			record.Set("device", device.Id)
			record.Set("status", "assigned")
			record.Set("from_number", device.GetString("phone_number"))
		} else {
			record.Set("status", "pending")
		}

		messages = append(messages, record)
	}

	// Persist atomically: a mid-batch Save failure rolls back the whole batch,
	// so we never leave dispatch-eligible rows behind while the caller refunds
	// the full quota reservation.
	if err := app.RunInTransaction(func(txApp core.App) error {
		for _, record := range messages {
			if err := txApp.Save(record); err != nil {
				return fmt.Errorf("failed to create message: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return messages, nil
}

// interpolateForRecipient builds the final message body for a single recipient.
func interpolateForRecipient(tmpl *TemplateOptions, phone string, contactMap map[string]*core.Record) string {
	// Start with custom variables
	vars := make(map[string]string, len(tmpl.Variables)+2)
	for k, v := range tmpl.Variables {
		vars[k] = v
	}

	// Add reserved variables from contact if available
	if contactMap != nil {
		if contact, ok := contactMap[phone]; ok {
			vars["name"] = contact.GetString("name")
			vars["phone"] = contact.GetString("phone_number")
		}
	}

	// Interpolate and strip invisible unicode
	result := InterpolateBody(tmpl.TemplateBody, vars)
	return StripInvisibleUnicode(result)
}

// statusToHookEvent maps an sms_messages.status terminal value to the
// outbound webhook event Vendel fires. Centralised so device ACKs, AEUM
// delivery webhooks and future provider DLR handlers stay consistent.
var statusToHookEvent = map[string]string{
	"sent":      "sms_sent",
	"delivered": "sms_delivered",
	"failed":    "sms_failed",
}

// MarkMessageTerminal applies a terminal lifecycle update to an sms_messages
// record: sets status, the matching timestamp and error_message, persists,
// and fires the outbound webhook. Callers may mutate other fields on msg
// before calling — those writes ride along in the same Save.
func MarkMessageTerminal(app core.App, msg *core.Record, status, errorMessage string) error {
	msg.Set("status", status)
	if errorMessage != "" {
		msg.Set("error_message", errorMessage)
	}
	switch status {
	case "sent":
		msg.Set("sent_at", types.NowDateTime())
	case "delivered":
		msg.Set("delivered_at", types.NowDateTime())
	}
	if err := app.Save(msg); err != nil {
		return err
	}
	if event, ok := statusToHookEvent[status]; ok {
		TriggerWebhooks(app, msg.GetString("user"), msg, event)
	}
	return nil
}

// ProcessSMSAck handles device acknowledgment for a sent SMS.
func ProcessSMSAck(app core.App, deviceId string, messageId string, status string, errorMessage string) error {
	record, err := app.FindRecordById("sms_messages", messageId)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}
	if record.GetString("device") != deviceId {
		return fmt.Errorf("message does not belong to this device")
	}

	// State transition guards (mirrors the AEUM event handler): duplicate
	// ACKs are idempotent no-ops so webhooks don't fire twice, and a
	// delivered message can never regress to failed.
	current := record.GetString("status")
	if current == status {
		return nil
	}
	if current == "delivered" {
		return fmt.Errorf("message already delivered; cannot transition to %q", status)
	}

	return MarkMessageTerminal(app, record, status, errorMessage)
}

// HandleIncomingSMS processes an incoming SMS from a device and triggers webhooks.
func HandleIncomingSMS(app core.App, userId string, deviceId string, fromNumber string, body string, timestamp string) (*core.Record, error) {
	cutoff := FilterTime(time.Now().UTC().Add(-5 * time.Minute))
	bodyHash, _ := ComputeBodyHash(body)
	existing, err := app.FindFirstRecordByFilter(
		"sms_messages",
		"message_type = 'incoming' && device = {:deviceId} && from_number = {:from} && body_hash = {:hash} && created >= {:cutoff}",
		dbx.Params{"deviceId": deviceId, "from": fromNumber, "hash": bodyHash, "cutoff": cutoff},
	)
	if err == nil && existing != nil {
		return existing, nil
	}

	collection, err := app.FindCollectionByNameOrId("sms_messages")
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("user", userId)
	record.Set("device", deviceId)
	record.Set("to", "")
	record.Set("from_number", fromNumber)
	record.Set("body", body)
	record.Set("status", "received")
	record.Set("message_type", "incoming")
	record.Set("webhook_sent", false)

	if err := app.Save(record); err != nil {
		return nil, err
	}

	routine.FireAndForget(func() { TriggerWebhooks(app, userId, record, "sms_received") })

	return record, nil
}
