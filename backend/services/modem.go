package services

import (
	"encoding/json"
	"log/slog"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/subscriptions"
)

// NotifyAgent sends an SSE event to agents subscribed to "<deviceType>/<deviceId>".
// deviceType must match one of the agent-backed device_type values ("modem", "smpp").
func NotifyAgent(app core.App, deviceType, deviceId string, record *core.Record) {
	topic := deviceType + "/" + deviceId

	data, err := json.Marshal(map[string]string{
		"message_id": record.Id,
		"recipient":  record.GetString("to"),
		"body":       GetRecordBody(record),
	})
	if err != nil {
		app.Logger().Error("failed to marshal agent SSE payload", slog.Any("error", err))
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
		app.Logger().Info("notified agent via SSE",
			slog.String("device_type", deviceType),
			slog.String("device", deviceId),
			slog.String("message", record.Id),
			slog.Int("subscribers", sent),
		)
	}
}

// BroadcastAgentStatus pushes the current online/offline state of all devices
// of the given type to frontend clients subscribed to "<deviceType>-status".
func BroadcastAgentStatus(app core.App, deviceType string) {
	devices, err := app.FindRecordsByFilter(
		"sms_devices",
		"device_type = {:type}",
		"", 0, 0,
		dbx.Params{"type": deviceType},
	)
	if err != nil || len(devices) == 0 {
		return
	}

	online := make(map[string]bool, len(devices))
	for _, d := range devices {
		topic := deviceType + "/" + d.Id
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

	statusTopic := deviceType + "-status"
	msg := subscriptions.Message{
		Name: statusTopic,
		Data: data,
	}
	for _, client := range app.SubscriptionsBroker().Clients() {
		if client.HasSubscription(statusTopic) {
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
		"-created", 50, 0,
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
