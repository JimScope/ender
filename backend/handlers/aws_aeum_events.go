// Package handlers — AWS End User Messaging delivery webhook.
//
// AEUM emits per-message delivery events through an SNS topic. SNS delivers
// those events to this endpoint via signed HTTPS POSTs. We verify the SNS
// signature, decode the inner AEUM event payload, find the matching
// sms_messages row by provider_message_id, mutate its status, and fire the
// corresponding outbound webhook.
package handlers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"vendel/services"
	"vendel/services/smsprovider"
)

// aeumStaleAfter is the window during which an orphan delivery event is
// treated as a race with the persisting goroutine in services.DispatchProviderMessages
// (we ask SNS to retry with 503). Past this window the UPDATE should already
// have landed, so a missing message means something is genuinely off — log
// and 200 to stop the SNS retry loop.
const aeumStaleAfter = 5 * time.Minute

// aeumExpectedTopicARN is loaded by RegisterAEUMEventRoutes from
// AEUM_SNS_TOPIC_ARN. Re-reading it on every request is unnecessary (env vars
// don't change at runtime); reading at mount-time is also test-friendly since
// each test re-runs RegisterAEUMEventRoutes after t.Setenv.
var aeumExpectedTopicARN string

func RegisterAEUMEventRoutes(se *core.ServeEvent) {
	aeumExpectedTopicARN = strings.TrimSpace(os.Getenv("AEUM_SNS_TOPIC_ARN"))
	se.Router.POST("/api/webhooks/aws-aeum-events", handleAEUMEvent)
}

func handleAEUMEvent(e *core.RequestEvent) error {
	raw, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return apis.NewBadRequestError("cannot read body", err)
	}

	var snsMsg smsprovider.SNSMessage
	if err := json.Unmarshal(raw, &snsMsg); err != nil {
		return apis.NewBadRequestError("invalid SNS payload", err)
	}

	if err := smsprovider.VerifySNSSignature(&snsMsg); err != nil {
		e.App.Logger().Warn("SNS signature verification failed", "err", err, "type", snsMsg.Type)
		return apis.NewUnauthorizedError("invalid SNS signature", err)
	}

	// SNS signatures only prove AWS signed the payload, not that the topic is
	// ours. Pin the expected topic ARN via AEUM_SNS_TOPIC_ARN; fail closed if
	// unset so subscription-confirmation spoofing is impossible.
	if aeumExpectedTopicARN == "" {
		e.App.Logger().Error("AEUM_SNS_TOPIC_ARN is not set; refusing AEUM webhook delivery. Set this env var to the SNS topic ARN that AEUM publishes to.", "type", snsMsg.Type, "topic", snsMsg.TopicARN)
		return apis.NewApiError(http.StatusInternalServerError, "AEUM webhook misconfigured: AEUM_SNS_TOPIC_ARN is required", nil)
	}
	if snsMsg.TopicARN != aeumExpectedTopicARN {
		e.App.Logger().Warn("rejecting SNS message from unexpected topic", "got", snsMsg.TopicARN, "expected", aeumExpectedTopicARN, "type", snsMsg.Type)
		return apis.NewForbiddenError("unexpected SNS topic", nil)
	}

	switch snsMsg.Type {
	case "SubscriptionConfirmation":
		if err := smsprovider.ConfirmSNSSubscription(snsMsg.SubscribeURL); err != nil {
			e.App.Logger().Error("SNS subscription confirm failed", "err", err, "topic", snsMsg.TopicARN)
			return apis.NewApiError(http.StatusBadGateway, "subscription confirm failed", err)
		}
		e.App.Logger().Info("SNS subscription confirmed", "topic", snsMsg.TopicARN)
		return e.JSON(http.StatusOK, map[string]string{"status": "confirmed"})

	case "UnsubscribeConfirmation":
		e.App.Logger().Warn("SNS unsubscribe received", "topic", snsMsg.TopicARN)
		return e.JSON(http.StatusOK, map[string]any{"status": "ok"})

	case "Notification":
		return handleAEUMNotification(e, &snsMsg)

	default:
		return apis.NewBadRequestError(fmt.Sprintf("unknown SNS message type %q", snsMsg.Type), nil)
	}
}

// aeumEventPayload models the subset of the inner AEUM event JSON Vendel
// actually consumes. originationPhoneNumber is the canonical field for the
// resolved sender; older payloads may still emit originationIdentity, so we
// accept both. Unknown fields tolerated.
type aeumEventPayload struct {
	EventType              string `json:"eventType"`
	MessageID              string `json:"messageId"`
	EventTimestampMS       int64  `json:"eventTimestamp"`
	OriginationPhoneNumber string `json:"originationPhoneNumber"`
	OriginationIdentity    string `json:"originationIdentity"`
}

func handleAEUMNotification(e *core.RequestEvent, snsMsg *smsprovider.SNSMessage) error {
	var ev aeumEventPayload
	if err := json.Unmarshal([]byte(snsMsg.Message), &ev); err != nil {
		return apis.NewBadRequestError("invalid AEUM event payload", err)
	}
	if ev.MessageID == "" {
		e.App.Logger().Warn("AEUM event missing messageId", "topic", snsMsg.TopicARN)
		return e.JSON(http.StatusOK, map[string]any{"status": "ignored"})
	}

	msg, _ := e.App.FindFirstRecordByFilter(
		"sms_messages",
		"provider_message_id = {:id}",
		dbx.Params{"id": ev.MessageID},
	)
	if msg == nil {
		eventTime := aeumEventTime(&ev, snsMsg)
		if time.Since(eventTime) < aeumStaleAfter {
			// Race: provider Send returned and SNS got the event before
			// services.DispatchProviderMessages persisted provider_message_id.
			// 503 makes SNS retry with exponential backoff.
			return apis.NewApiError(http.StatusServiceUnavailable, "message not yet persisted; retry later", nil)
		}
		e.App.Logger().Error("orphan AEUM event", "messageId", ev.MessageID, "eventType", ev.EventType, "eventTime", eventTime)
		return e.JSON(http.StatusOK, map[string]any{"status": "orphan"})
	}

	newStatus := mapAEUMEventType(ev.EventType)
	if newStatus == "" {
		return e.JSON(http.StatusOK, map[string]any{"status": "ignored"})
	}
	// AEUM and SNS both retry on 5xx, so duplicate notifications for a
	// terminal status are routine. Skip the UPDATE + webhook fanout when
	// the status is already what the event reports.
	if msg.GetString("status") == newStatus {
		return e.JSON(http.StatusOK, map[string]any{"status": "ok"})
	}

	// AEUM emits TEXT_SUCCESSFUL for both SMS and native RCS deliveries; the
	// channel must come from the resolved sender shape, not the event prefix.
	identity := cmp.Or(ev.OriginationPhoneNumber, ev.OriginationIdentity)
	if identity != "" {
		msg.Set("provider_origination_identity", identity)
	}
	if ch := deriveProviderChannel(identity); ch != "" {
		msg.Set("provider_channel", ch)
	}

	if err := services.MarkMessageTerminal(e.App, msg, newStatus, ""); err != nil {
		e.App.Logger().Error("update message from AEUM event failed", "err", err, "messageId", ev.MessageID)
		return apis.NewApiError(http.StatusInternalServerError, "internal update failed", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"status": "ok"})
}

func aeumEventTime(ev *aeumEventPayload, snsMsg *smsprovider.SNSMessage) time.Time {
	if ev.EventTimestampMS > 0 {
		return time.Unix(0, ev.EventTimestampMS*int64(time.Millisecond))
	}
	if t, err := time.Parse(time.RFC3339Nano, snsMsg.Timestamp); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, snsMsg.Timestamp); err == nil {
		return t
	}
	return time.Now()
}

// mapAEUMEventType collapses the AEUM event taxonomy into Vendel's terminal
// delivered/failed status. Pending/queued/sent and any unrecognised type
// return "" so the caller treats them as no-ops — future AEUM additions
// degrade to "ignored". The outbound webhook event is derived from status by
// services.MarkMessageTerminal.
func mapAEUMEventType(t string) string {
	switch t {
	case "TEXT_DELIVERED", "TEXT_SUCCESSFUL":
		return smsprovider.StatusDelivered
	case "TEXT_TTL_EXPIRED",
		"TEXT_BLOCKED",
		"TEXT_CARRIER_UNREACHABLE",
		"TEXT_INVALID",
		"TEXT_INVALID_MESSAGE",
		"TEXT_UNKNOWN",
		"TEXT_UNREACHABLE",
		"TEXT_CARRIER_BLOCKED",
		"TEXT_SPAM",
		"TEXT_PROTECT_BLOCKED":
		return smsprovider.StatusFailed
	default:
		return ""
	}
}

// deriveProviderChannel infers SMS vs RCS from the shape of the resolved
// sender. AEUM emits TEXT_SUCCESSFUL for native RCS too, so the eventType
// prefix is unreliable; the shape of originationPhoneNumber is the actual
// signal:
//   - "+..." (E.164 long/toll-free/10DLC) → SMS
//   - bare digits (short code, numeric sender id)  → SMS
//   - anything else (RCS agent ARN, agent name)    → RCS
//
// Pure-alphanumeric SMS sender IDs ("VENDEL") remain ambiguous and may be
// reported as RCS; that is acceptable for the MVP since AEUM normally exposes
// numeric origins in this field.
func deriveProviderChannel(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ""
	}
	if strings.HasPrefix(identity, "+") || isAllDigits(identity) {
		return smsprovider.ChannelSMS
	}
	return smsprovider.ChannelRCS
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
