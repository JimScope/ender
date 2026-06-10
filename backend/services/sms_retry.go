package services

import (
	"log/slog"
	"strings"
	"time"
	"vendel/services/smsprovider"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// isPermanentFailure returns true for errors that should not be retried.
// "provider delivery failure" covers terminal delivery events reported by an
// external provider (e.g. AEUM via SNS): the provider already exhausted its
// own retries, so re-dispatching would only bill another SMS.
func isPermanentFailure(errMsg string) bool {
	permanent := []string{
		"invalid number",
		"blocked",
		"unsubscribed",
		"blacklisted",
		"not a valid phone",
		"provider delivery failure",
	}
	lower := strings.ToLower(errMsg)
	for _, p := range permanent {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// RetryFailedMessages retries failed outgoing messages with exponential backoff
// and a maximum of SMSMaxRetries attempts. Permanent failures are skipped.
// Retried messages go back to "assigned" and are re-dispatched (FCM tickle /
// provider send); modem agents are notified by the realtime hook on save.
// It also rescues "pending" messages created when the user had no device,
// assigning them as soon as a device becomes available.
func RetryFailedMessages(app core.App) error {
	cutoff := FilterTime(time.Now().UTC().Add(-SMSRetryCutoff))

	records, err := app.FindRecordsByFilter(
		"sms_messages",
		"status = 'failed' && message_type = 'outgoing' && retry_count < {:maxRetries} && created >= {:cutoff}",
		"", 50, 0,
		dbx.Params{"maxRetries": SMSMaxRetries, "cutoff": cutoff},
	)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	retried := 0
	skipped := 0
	requeued := make([]*core.Record, 0, len(records))
	for _, record := range records {
		// Skip permanent failures
		if isPermanentFailure(record.GetString("error_message")) {
			skipped++
			continue
		}

		// Enforce exponential backoff based on retry_count
		retryCount := record.GetInt("retry_count")
		if retryCount > 0 && retryCount <= len(SMSRetryBackoffs) {
			lastRetry := record.GetDateTime("last_retry_at").Time()
			if !lastRetry.IsZero() {
				requiredWait := SMSRetryBackoffs[retryCount-1]
				if now.Sub(lastRetry) < requiredWait {
					continue // not enough time has passed
				}
			}
		}

		// Messages can lose their device (e.g. it was deleted) — re-resolve
		// so the retry has a transport to go through.
		if record.GetString("device") == "" {
			if !assignAvailableDevice(app, record) {
				continue
			}
		}

		record.Set("status", "assigned")
		record.Set("retry_count", retryCount+1)
		record.Set("last_retry_at", types.NowDateTime())
		record.Set("error_message", "")
		if err := app.Save(record); err == nil {
			retried++
			requeued = append(requeued, record)
		}
	}

	requeued = append(requeued, rescuePendingMessages(app, cutoff)...)
	requeued = append(requeued, rescueStaleSendingMessages(app)...)

	// Re-dispatch outside the loop: one FCM tickle per device, one provider
	// batch per type, instead of per-message traffic.
	if len(requeued) > 0 {
		dispatchOutgoing(app, requeued)
	}

	app.Logger().Info("Retried failed SMS messages",
		slog.Int("retried", retried), slog.Int("skipped_permanent", skipped))
	return nil
}

// rescuePendingMessages assigns devices to outgoing messages stuck in
// "pending" (created while the user had no usable device). Messages older
// than the retry cutoff are marked failed so their state is honest and the
// sms_failed webhook fires.
func rescuePendingMessages(app core.App, cutoff string) []*core.Record {
	records, err := app.FindRecordsByFilter(
		"sms_messages",
		"status = 'pending' && message_type = 'outgoing'",
		"", 50, 0,
	)
	if err != nil {
		app.Logger().Warn("failed to query pending messages", slog.Any("error", err))
		return nil
	}

	rescued := make([]*core.Record, 0, len(records))
	for _, record := range records {
		if assignAvailableDevice(app, record) {
			record.Set("status", "assigned")
			if err := app.Save(record); err == nil {
				rescued = append(rescued, record)
			}
			continue
		}

		// Still no device — fail messages past the retry window instead of
		// leaving them in limbo forever.
		if record.GetString("created") < cutoff {
			if err := MarkMessageTerminal(app, record, "failed", "no device available"); err != nil {
				app.Logger().Warn("failed to expire pending message",
					slog.String("message", record.Id), slog.Any("error", err))
			}
		}
	}
	return rescued
}

// rescueStaleSendingMessages re-queues outgoing messages stuck in "sending".
// Agents move assigned → sending when they claim a batch via
// /api/sms/pending; if the agent dies before sending (or before reporting),
// nothing else ever touches the record — pending fetches only return
// "assigned" — so without this rescue the message is stranded forever.
// Re-queueing after the stale window accepts a small duplicate-SMS risk
// (sent but never reported) in exchange for never losing a message.
func rescueStaleSendingMessages(app core.App) []*core.Record {
	staleCutoff := FilterTime(time.Now().UTC().Add(-SMSSendingStaleAfter))

	records, err := app.FindRecordsByFilter(
		"sms_messages",
		"status = 'sending' && message_type = 'outgoing' && updated < {:staleCutoff}",
		"", 50, 0,
		dbx.Params{"staleCutoff": staleCutoff},
	)
	if err != nil {
		app.Logger().Warn("failed to query stale sending messages", slog.Any("error", err))
		return nil
	}

	rescued := make([]*core.Record, 0, len(records))
	for _, record := range records {
		retryCount := record.GetInt("retry_count")
		if retryCount >= SMSMaxRetries {
			if err := MarkMessageTerminal(app, record, "failed", "agent claimed the message but never reported a result"); err != nil {
				app.Logger().Warn("failed to expire stale sending message",
					slog.String("message", record.Id), slog.Any("error", err))
			}
			continue
		}

		record.Set("status", "assigned")
		record.Set("retry_count", retryCount+1)
		record.Set("last_retry_at", types.NowDateTime())
		if err := app.Save(record); err == nil {
			rescued = append(rescued, record)
		}
	}
	if len(rescued) > 0 {
		app.Logger().Info("rescued stale sending messages", slog.Int("count", len(rescued)))
	}
	return rescued
}

// assignAvailableDevice resolves a device for the message's user and sets the
// device + from_number fields. Returns false when no device is available.
func assignAvailableDevice(app core.App, record *core.Record) bool {
	devices, err := resolveDevices(app, record.GetString("user"), "", smsprovider.DefaultAEUM())
	if err != nil || len(devices) == 0 {
		return false
	}
	device := devices[0]
	record.Set("device", device.Id)
	record.Set("from_number", device.GetString("phone_number"))
	return true
}
