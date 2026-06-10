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

// RegisterRealtimeHooks registers modem notification on sms_messages
// assignment, subscription guards for modem/* topics, and modem status
// broadcasts on client connect/disconnect.
func RegisterRealtimeHooks(app *pocketbase.PocketBase) {
	// SMS Messages: notify modem agents via SSE when messages are assigned
	notifyModemIfAssigned := func(e *core.RecordEvent) error {
		if e.Record.GetString("status") != "assigned" {
			return e.Next()
		}
		deviceId := e.Record.GetString("device")
		if deviceId == "" {
			return e.Next()
		}
		device, err := e.App.FindRecordById("sms_devices", deviceId)
		if err != nil || device.GetString("device_type") != "modem" {
			return e.Next()
		}
		routine.FireAndForget(func() { services.NotifyModemAgent(e.App, deviceId, e.Record) })
		return e.Next()
	}
	app.OnRecordAfterCreateSuccess("sms_messages").BindFunc(notifyModemIfAssigned)
	app.OnRecordAfterUpdateSuccess("sms_messages").BindFunc(notifyModemIfAssigned)

	// Realtime: guard modem/* subscriptions and broadcast status on relevant subscriptions
	app.OnRealtimeSubscribeRequest().BindFunc(func(e *core.RealtimeSubscribeRequestEvent) error {
		hasModemSub := false
		hasStatusSub := false
		for _, sub := range e.Subscriptions {
			if sub == "modem-status" {
				hasStatusSub = true
				continue
			}
			if !strings.HasPrefix(sub, "modem/") {
				continue
			}
			hasModemSub = true
			deviceId := strings.TrimPrefix(sub, "modem/")
			apiKey := e.Request.Header.Get("X-API-Key")
			if apiKey == "" {
				return fmt.Errorf("authentication required for modem subscriptions")
			}
			// Match the hashed api_key (with legacy plaintext fallback) via the
			// same helper the device-auth middleware uses, so a future hashing
			// change can never desync these two call sites again.
			_, err := services.FindByAPIKey(
				e.App,
				"sms_devices",
				"id = {:id} && api_key = {:key}",
				apiKey,
				dbx.Params{"id": deviceId},
			)
			if err != nil {
				return fmt.Errorf("unauthorized modem subscription")
			}
		}
		if err := e.Next(); err != nil {
			return err
		}
		// Broadcast modem status when an agent connects or a frontend subscribes
		if hasModemSub || hasStatusSub {
			routine.FireAndForget(func() { services.BroadcastModemStatus(e.App) })
		}
		return nil
	})

	// Realtime: broadcast modem status when any SSE client disconnects
	app.OnRealtimeConnectRequest().BindFunc(func(e *core.RealtimeConnectRequestEvent) error {
		// e.Next() blocks until the client disconnects
		if err := e.Next(); err != nil {
			return err
		}
		// Client disconnected — broadcast updated modem status
		routine.FireAndForget(func() { services.BroadcastModemStatus(e.App) })
		return nil
	})
}
