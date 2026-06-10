package services

import (
	"encoding/json"
	"log/slog"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/subscriptions"
)

// NotifyModemAgent sends an SSE event to modem agents subscribed to "modem/<deviceId>".
func NotifyModemAgent(app core.App, deviceId string, record *core.Record) {
	topic := "modem/" + deviceId

	data, err := json.Marshal(map[string]string{
		"message_id": record.Id,
		"recipient":  record.GetString("to"),
		"body":       GetRecordBody(record),
	})
	if err != nil {
		app.Logger().Error("failed to marshal modem SSE payload", slog.Any("error", err))
		return
	}

	msg := subscriptions.Message{
		Name: topic,
		Data: data,
	}

	sent := 0
	for _, client := range app.SubscriptionsBroker().Clients() {
		if client.HasSubscription(topic) {
			client.Send(msg)
			sent++
		}
	}

	if sent > 0 {
		app.Logger().Info("notified modem agent via SSE",
			slog.String("device", deviceId),
			slog.String("message", record.Id),
			slog.Int("subscribers", sent),
		)
	}
}

// BroadcastModemStatus pushes the current online/offline state of all modem devices
// to frontend clients subscribed to the "modem-status" topic.
func BroadcastModemStatus(app core.App) {
	devices, err := app.FindRecordsByFilter(
		"sms_devices",
		"device_type = 'modem'",
		"", 0, 0,
	)
	if err != nil || len(devices) == 0 {
		return
	}

	online := make(map[string]bool, len(devices))
	for _, d := range devices {
		topic := "modem/" + d.Id
		connected := false
		for _, client := range app.SubscriptionsBroker().Clients() {
			if client.HasSubscription(topic) {
				connected = true
				break
			}
		}
		online[d.Id] = connected
	}

	data, err := json.Marshal(online)
	if err != nil {
		return
	}

	msg := subscriptions.Message{
		Name: "modem-status",
		Data: data,
	}
	for _, client := range app.SubscriptionsBroker().Clients() {
		if client.HasSubscription("modem-status") {
			client.Send(msg)
		}
	}
}

// ClaimPendingMessages atomically marks assigned messages as "sending" for a device.
// Used on agent startup to recover any messages assigned while the agent was offline.
func ClaimPendingMessages(app core.App, deviceId string) ([]*core.Record, error) {
	records, err := app.FindRecordsByFilter(
		"sms_messages",
		"device = {:deviceId} && status = 'assigned' && message_type = 'outgoing'",
		"-created", SMSClaimBatchSize, 0,
		dbx.Params{"deviceId": deviceId},
	)
	if err != nil {
		return nil, err
	}

	claimed := make([]*core.Record, 0, len(records))
	for _, r := range records {
		r.Set("status", "sending")
		if err := app.Save(r); err == nil {
			claimed = append(claimed, r)
		}
	}
	return claimed, nil
}
