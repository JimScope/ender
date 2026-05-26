package services

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/routine"
	"github.com/pocketbase/pocketbase/tools/types"
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

	if err := CheckSMSQuota(app, userId, len(recipients)); err != nil {
		return nil, err
	}

	devices, err := resolveDevices(app, userId, deviceId)
	if err != nil {
		return nil, err
	}

	// Build contact lookup for template interpolation
	var contactMap map[string]*core.Record
	if tmpl != nil {
		contactMap = buildContactMap(app, userId, recipients)
	}

	messages, err := createMessageRecords(app, userId, recipients, body, devices, tmpl, contactMap)
	if err != nil {
		return nil, err
	}

	if err := IncrementSMSCount(app, userId, len(recipients)); err != nil {
		app.Logger().Warn("failed to increment SMS count", slog.Any("error", err))
	}

	if len(devices) > 0 {
		routine.FireAndForget(func() { DispatchMessages(app, messages) })
	}

	return messages, nil
}

// resolveDevices returns the target device(s) for sending.
func resolveDevices(app core.App, userId, deviceId string) ([]*core.Record, error) {
	if deviceId != "" {
		device, err := app.FindRecordById("sms_devices", deviceId)
		if err != nil {
			return nil, fmt.Errorf("device not found: %w", err)
		}
		if device.GetString("user") != userId {
			return nil, fmt.Errorf("device does not belong to user")
		}
		return []*core.Record{device}, nil
	}

	records, err := app.FindRecordsByFilter(
		"sms_devices",
		"user = {:userId} && (fcm_token != '' || device_type = 'modem' || device_type = 'smpp')",
		"-created",
		0, 0,
		dbx.Params{"userId": userId},
	)
	if err != nil || len(records) == 0 {
		return nil, nil
	}
	return records, nil
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

		if err := app.Save(record); err != nil {
			return nil, fmt.Errorf("failed to create message: %w", err)
		}
		messages = append(messages, record)
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

// ProcessSMSAck handles device acknowledgment for a sent SMS.
func ProcessSMSAck(app core.App, deviceId string, messageId string, status string, errorMessage string) error {
	record, err := app.FindRecordById("sms_messages", messageId)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	if record.GetString("device") != deviceId {
		return fmt.Errorf("message does not belong to this device")
	}

	record.Set("status", status)
	if errorMessage != "" {
		record.Set("error_message", errorMessage)
	}

	switch status {
	case "sent":
		record.Set("sent_at", types.NowDateTime())
	case "delivered":
		record.Set("delivered_at", types.NowDateTime())
	}

	if err := app.Save(record); err != nil {
		return err
	}

	eventMap := map[string]string{"sent": "sms_sent", "delivered": "sms_delivered", "failed": "sms_failed"}
	if event, ok := eventMap[status]; ok {
		routine.FireAndForget(func() { TriggerWebhooks(app, record.GetString("user"), record, event) })
	}

	return nil
}

// HandleIncomingSMS processes an incoming SMS from a device and triggers webhooks.
func HandleIncomingSMS(app core.App, userId string, deviceId string, fromNumber string, body string, timestamp string) (*core.Record, error) {
	cutoff := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
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
