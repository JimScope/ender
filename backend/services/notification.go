package services

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/routine"
	"google.golang.org/api/option"
)

var fcmClient *messaging.Client
var fcmBreaker = NewCircuitBreaker("fcm", 5, 60*time.Second, 5*time.Minute)

// InitFCM initializes the Firebase Admin SDK from environment.
func InitFCM(pbApp core.App) {
	credJSON := os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON")
	if credJSON == "" {
		pbApp.Logger().Warn("FIREBASE_SERVICE_ACCOUNT_JSON not set, FCM disabled")
		return
	}

	ctx := context.Background()
	fbApp, err := firebase.NewApp(ctx, nil, option.WithCredentialsJSON([]byte(credJSON)))
	if err != nil {
		pbApp.Logger().Error("failed to initialize Firebase app", slog.Any("error", err))
		return
	}

	client, err := fbApp.Messaging(ctx)
	if err != nil {
		pbApp.Logger().Error("failed to get FCM client", slog.Any("error", err))
		return
	}

	fcmClient = client
	pbApp.Logger().Info("Firebase Admin SDK initialized")
}

// DispatchMessages groups messages by device and sends FCM tickle notifications.
// The FCM payload contains no sensitive data (no message body or recipients).
// Devices fetch actual messages via GET /api/sms/pending after receiving the tickle.
func DispatchMessages(app core.App, messages []*core.Record) {
	if len(messages) == 0 {
		return
	}

	// Group by device
	byDevice := make(map[string][]*core.Record)
	for _, msg := range messages {
		deviceId := msg.GetString("device")
		if deviceId != "" {
			byDevice[deviceId] = append(byDevice[deviceId], msg)
		}
	}

	for deviceId, deviceMessages := range byDevice {
		device, err := app.FindRecordById("sms_devices", deviceId)
		if err != nil {
			app.Logger().Warn("device not found", slog.String("device", deviceId), slog.Any("error", err))
			continue
		}

		// Modem devices receive messages via SSE, not FCM
		if device.GetString("device_type") == "modem" {
			continue
		}

		fcmToken := device.GetString("fcm_token")
		if fcmToken == "" {
			app.Logger().Warn("device has no FCM token", slog.String("device", deviceId))
			continue
		}

		// Send a single tickle per device with the message count
		count := len(deviceMessages)
		messageIds := make([]string, 0, count)
		for _, msg := range deviceMessages {
			messageIds = append(messageIds, msg.Id)
		}

		routine.FireAndForget(func() {
			if !fcmBreaker.Allow() {
				app.Logger().Warn("FCM circuit breaker open, failing dispatch",
					slog.String("token", tokenPrefix(fcmToken)))
				// Mark failed so the retry cron re-assigns and re-dispatches
				// them once the breaker closes ("assigned" has no consumer).
				for _, id := range messageIds {
					markMessageFailed(app, id, "FCM circuit breaker open")
				}
				return
			}
			if err := sendFCMTickle(fcmToken, count); err != nil {
				fcmBreaker.RecordFailure()
				app.Logger().Error("FCM send failed", slog.String("token", tokenPrefix(fcmToken)), slog.Any("error", err))
				for _, id := range messageIds {
					markMessageFailed(app, id, err.Error())
				}
			} else {
				fcmBreaker.RecordSuccess()
			}
		})
	}
}

// sendFCMTickle sends a data-only FCM message to wake up the device.
// No sensitive data (message body, recipients) is included in the payload.
func sendFCMTickle(token string, count int) error {
	if fcmClient == nil {
		return fmt.Errorf("FCM not initialized")
	}

	msg := &messaging.Message{
		Token: token,
		Data: map[string]string{
			"type":  "tickle",
			"count": strconv.Itoa(count),
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), FCMContextTimeout)
	defer cancel()

	resp, err := fcmClient.Send(ctx, msg)
	if err != nil {
		return err
	}

	log.Printf("FCM tickle sent: %s", resp)
	return nil
}

// tokenPrefix returns the first 20 chars of a token for log identification,
// without panicking on short tokens.
func tokenPrefix(token string) string {
	return token[:min(len(token), 20)]
}

func markMessageFailed(app core.App, messageId, errMsg string) {
	record, err := app.FindRecordById("sms_messages", messageId)
	if err != nil {
		return
	}
	record.Set("status", "failed")
	record.Set("error_message", errMsg)
	if err := app.Save(record); err != nil {
		app.Logger().Warn("failed to save failed message", slog.Any("error", err))
		return
	}
	routine.FireAndForget(func() { TriggerWebhooks(app, record.GetString("user"), record, "sms_failed") })
}

