package hooks

import (
	"fmt"
	"strings"
	"vendel/services"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/routine"
)

// agentTopicPrefixes lists the device types that speak to the backend via SSE.
// For each, the broadcast topic for messages is "<type>/<deviceId>" and the
// status broadcast topic is "<type>-status".
var agentTopicPrefixes = []string{"modem", "smpp"}

// RegisterRealtimeHooks registers agent notification on sms_messages
// assignment, subscription guards for <agent>/* topics, and agent status
// broadcasts on client connect/disconnect.
func RegisterRealtimeHooks(app *pocketbase.PocketBase) {
	// SMS Messages: notify agents via SSE when messages are assigned
	notifyAgentIfAssigned := func(e *core.RecordEvent) error {
		if e.Record.GetString("status") != "assigned" {
			return e.Next()
		}
		deviceId := e.Record.GetString("device")
		if deviceId == "" {
			return e.Next()
		}
		device, err := e.App.FindRecordById("sms_devices", deviceId)
		if err != nil {
			return e.Next()
		}
		dt := device.GetString("device_type")
		if !isAgentBacked(dt) {
			return e.Next()
		}
		record := e.Record
		routine.FireAndForget(func() { services.NotifyAgent(e.App, dt, deviceId, record) })
		return e.Next()
	}
	app.OnRecordAfterCreateSuccess("sms_messages").BindFunc(notifyAgentIfAssigned)
	app.OnRecordAfterUpdateSuccess("sms_messages").BindFunc(notifyAgentIfAssigned)

	// Realtime: guard <agent>/* subscriptions and broadcast status on relevant subscriptions
	app.OnRealtimeSubscribeRequest().BindFunc(func(e *core.RealtimeSubscribeRequestEvent) error {
		touchedTypes := make(map[string]bool)
		statusTopics := make(map[string]bool)

		for _, sub := range e.Subscriptions {
			if dt, ok := trimStatusSuffix(sub); ok {
				statusTopics[dt] = true
				continue
			}
			dt, deviceId, ok := parseAgentTopic(sub)
			if !ok {
				continue
			}
			touchedTypes[dt] = true
			apiKey := e.Request.Header.Get("X-API-Key")
			if apiKey == "" {
				return fmt.Errorf("authentication required for %s subscriptions", dt)
			}
			_, err := e.App.FindFirstRecordByFilter(
				"sms_devices",
				"id = {:id} && api_key = {:key}",
				dbx.Params{"id": deviceId, "key": apiKey},
			)
			if err != nil {
				return fmt.Errorf("unauthorized %s subscription", dt)
			}
		}
		if err := e.Next(); err != nil {
			return err
		}
		// Broadcast status per touched type when an agent connects or a frontend subscribes
		for dt := range touchedTypes {
			dt := dt
			routine.FireAndForget(func() { services.BroadcastAgentStatus(e.App, dt) })
		}
		for dt := range statusTopics {
			dt := dt
			routine.FireAndForget(func() { services.BroadcastAgentStatus(e.App, dt) })
		}
		return nil
	})

	// Realtime: broadcast status for every agent-backed type when any SSE client disconnects
	app.OnRealtimeConnectRequest().BindFunc(func(e *core.RealtimeConnectRequestEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		for _, dt := range agentTopicPrefixes {
			dt := dt
			routine.FireAndForget(func() { services.BroadcastAgentStatus(e.App, dt) })
		}
		return nil
	})
}

func isAgentBacked(deviceType string) bool {
	for _, t := range agentTopicPrefixes {
		if t == deviceType {
			return true
		}
	}
	return false
}

// parseAgentTopic extracts (deviceType, deviceId) from "<type>/<deviceId>".
func parseAgentTopic(topic string) (string, string, bool) {
	for _, t := range agentTopicPrefixes {
		prefix := t + "/"
		if strings.HasPrefix(topic, prefix) {
			id := strings.TrimPrefix(topic, prefix)
			if id == "" {
				return "", "", false
			}
			return t, id, true
		}
	}
	return "", "", false
}

// trimStatusSuffix matches "<type>-status" topics.
func trimStatusSuffix(topic string) (string, bool) {
	const suffix = "-status"
	if !strings.HasSuffix(topic, suffix) {
		return "", false
	}
	dt := strings.TrimSuffix(topic, suffix)
	if !isAgentBacked(dt) {
		return "", false
	}
	return dt, true
}
