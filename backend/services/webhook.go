package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/routine"
)

// webhookTransport is a shared HTTP transport for webhook delivery,
// enabling connection reuse across requests.
// Uses a custom DialContext to re-validate resolved IPs at connection time,
// preventing DNS rebinding attacks (TOCTOU between ValidateWebhookURL and connect).
var webhookTransport = &http.Transport{
	MaxIdleConns:        WebhookMaxIdleConns,
	MaxIdleConnsPerHost: WebhookMaxIdlePerHost,
	IdleConnTimeout:     WebhookIdleConnTimeout,
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", addr, err)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS resolution failed for %q: %w", host, err)
		}
		for _, ip := range ips {
			if isPrivateIP(ip.IP) {
				return nil, fmt.Errorf("blocked: %q resolves to private IP", host)
			}
		}
		dialer := &net.Dialer{Timeout: WebhookDialTimeout}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	},
}

// webhookRetryBackoffs aliases the shared constant for internal use.
var webhookRetryBackoffs = WebhookRetryBackoffs

// privateRanges defines IP ranges that should be blocked for webhook URLs.
var privateRanges []*net.IPNet

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"169.254.0.0/16", // Link-local
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique local
	} {
		_, network, _ := net.ParseCIDR(cidr)
		privateRanges = append(privateRanges, network)
	}
}

func isPrivateIP(ip net.IP) bool {
	for _, network := range privateRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return ip.IsUnspecified() || ip.IsMulticast()
}

// ValidateWebhookURL checks that a URL is safe to use as a webhook target.
// It blocks non-HTTP(S) schemes and destinations that resolve to private/reserved IPs.
func ValidateWebhookURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("invalid URL scheme %q: only http and https are allowed", parsed.Scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no hostname")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname %q: %w", host, err)
	}

	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("webhook URL resolves to private/reserved IP address")
		}
	}

	return nil
}

// WebhookDeliveryResult holds the outcome of a webhook delivery attempt.
type WebhookDeliveryResult struct {
	LogRecord      *core.Record
	DeliveryStatus string
	ResponseStatus int
	DurationMs     int
	ErrorMessage   string
}

// ToJSON returns the result as a map suitable for JSON responses.
func (r *WebhookDeliveryResult) ToJSON() map[string]any {
	resp := map[string]any{
		"delivery_status": r.DeliveryStatus,
		"response_status": r.ResponseStatus,
		"duration_ms":     r.DurationMs,
		"error_message":   r.ErrorMessage,
	}
	if r.LogRecord != nil {
		resp["log_id"] = r.LogRecord.Id
	}
	return resp
}

// buildMessagePayload constructs the webhook payload for an SMS message
// event. Used both for the initial delivery and to regenerate the payload on
// retries (the stored request_body is PII-redacted and must never be re-sent).
func buildMessagePayload(webhook, message *core.Record, event string) map[string]any {
	payload := map[string]any{
		"event":      event,
		"message_id": message.Id,
		"timestamp":  message.GetString("created"),
	}

	if webhook.GetBool("include_body") {
		payload["body"] = GetRecordBody(message)
	}

	switch event {
	case "sms_received":
		payload["from"] = message.GetString("from_number")
	case "sms_sent", "sms_delivered", "sms_failed":
		payload["to"] = message.GetString("to")
		payload["status"] = message.GetString("status")
		if v := message.GetString("error_message"); v != "" {
			payload["error_message"] = v
		}
		if v := message.GetString("sent_at"); v != "" {
			payload["sent_at"] = v
		}
		if v := message.GetString("delivered_at"); v != "" {
			payload["delivered_at"] = v
		}
	}

	return payload
}

// SendWebhookForMessage delivers a webhook HTTP POST for an SMS message.
// It does not mutate message — marking webhook_sent is the caller's job
// (core.Record is not safe for concurrent mutation).
func SendWebhookForMessage(app core.App, webhook *core.Record, message *core.Record, event string) error {
	if !webhook.GetBool("active") {
		return fmt.Errorf("webhook inactive")
	}

	payload := buildMessagePayload(webhook, message, event)
	result := deliverWebhook(app, webhook, payload, event)

	if result.DeliveryStatus == "failed" {
		return fmt.Errorf("webhook delivery failed: %s", result.ErrorMessage)
	}

	return nil
}

// SendTestWebhook sends a synthetic test payload to the webhook and returns the delivery result.
// Intentionally does not consult webhookBreaker: the test endpoint is a manual
// override so a user can probe a host whose production traffic has tripped the
// breaker. Result still recorded in webhook_delivery_logs for visibility.
func SendTestWebhook(app core.App, webhook *core.Record) *WebhookDeliveryResult {
	payload := map[string]any{
		"event":      "test",
		"message_id": "test_" + GenerateSecureKey("", 12),
		"body":       "Test webhook from " + DefaultAppName,
		"from":       "+1234567890",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}

	return deliverWebhook(app, webhook, payload, "test")
}

// webhookHTTPResult holds the raw outcome of a single webhook HTTP attempt.
type webhookHTTPResult struct {
	ResponseStatus int
	ResponseBody   string
	DeliveryStatus string // "success" or "failed"
	ErrorMessage   string
	DurationMs     int
}

// executeWebhookRequest performs the signed HTTP POST and returns the raw
// outcome WITHOUT writing any delivery log. Initial deliveries log via
// deliverWebhook; retries update their existing log record instead of
// spawning new ones (which previously caused exponential retry amplification).
func executeWebhookRequest(app core.App, webhook *core.Record, payload map[string]any) *webhookHTTPResult {
	url := webhook.GetString("url")
	failed := func(errMsg string, durationMs int) *webhookHTTPResult {
		return &webhookHTTPResult{DeliveryStatus: "failed", ErrorMessage: errMsg, DurationMs: durationMs}
	}

	// SSRF protection: validate URL before making any request
	if err := ValidateWebhookURL(url); err != nil {
		return failed(fmt.Sprintf("blocked: %v", err), 0)
	}

	payloadJSON, err := marshalSorted(payload)
	if err != nil {
		return failed(fmt.Sprintf("marshal payload: %v", err), 0)
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// HMAC-SHA256 signature if secret is configured. A decryption failure
	// (e.g. rotated/lost WEBHOOK_ENCRYPTION_KEY) must fail loudly: signing
	// with the raw ciphertext would silently break signature verification
	// on the receiver side.
	secretKey := webhook.GetString("secret_key")
	if secretKey != "" {
		decrypted, err := DecryptSecret(secretKey)
		if err != nil {
			app.Logger().Error("webhook secret decryption failed — delivery aborted (check WEBHOOK_ENCRYPTION_KEY)",
				slog.String("webhook", webhook.Id), slog.Any("error", err))
			return failed("webhook secret decryption failed (check WEBHOOK_ENCRYPTION_KEY)", 0)
		}
		sig := generateHMAC(decrypted, string(payloadJSON))
		headers["X-Webhook-Signature"] = sig
	}

	// Get timeout from system config (capped)
	timeout := WebhookDefaultTimeout
	config, err := app.FindFirstRecordByFilter("system_config", "key = 'webhook_timeout'")
	if err == nil && config != nil {
		if t := config.GetInt("value"); t > 0 {
			timeout = t
		}
	}
	if timeout > WebhookMaxTimeout {
		timeout = WebhookMaxTimeout
	}

	client := &http.Client{
		Timeout:   time.Duration(timeout) * time.Second,
		Transport: webhookTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= WebhookMaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if err := ValidateWebhookURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(payloadJSON))
	if err != nil {
		return failed(fmt.Sprintf("create request: %v", err), 0)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := client.Do(req)
	durationMs := int(time.Since(start).Milliseconds())

	if err != nil {
		return failed(fmt.Sprintf("request failed: %v", err), durationMs)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, WebhookResponseMaxBytes))
	respBodyStr := string(respBody)
	if len(respBodyStr) > WebhookResponseMaxChars {
		respBodyStr = respBodyStr[:WebhookResponseMaxChars]
	}

	result := &webhookHTTPResult{
		ResponseStatus: resp.StatusCode,
		ResponseBody:   respBodyStr,
		DeliveryStatus: "success",
		DurationMs:     durationMs,
	}
	if resp.StatusCode >= 400 {
		result.DeliveryStatus = "failed"
		result.ErrorMessage = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	return result
}

// deliverWebhook performs the HTTP request, measures timing, and logs the delivery.
func deliverWebhook(app core.App, webhook *core.Record, payload map[string]any, event string) *WebhookDeliveryResult {
	res := executeWebhookRequest(app, webhook, payload)
	return logDelivery(app, webhook, event, webhook.GetString("url"), payload,
		res.ResponseStatus, res.ResponseBody, res.DeliveryStatus, res.ErrorMessage, res.DurationMs)
}

// logDelivery creates a webhook_delivery_logs record and returns the result.
func logDelivery(app core.App, webhook *core.Record, event, url string, payload map[string]any, responseStatus int, responseBody, deliveryStatus, errMsg string, durationMs int) *WebhookDeliveryResult {
	result := &WebhookDeliveryResult{
		DeliveryStatus: deliveryStatus,
		ResponseStatus: responseStatus,
		DurationMs:     durationMs,
		ErrorMessage:   errMsg,
	}

	col, err := app.FindCollectionByNameOrId("webhook_delivery_logs")
	if err != nil {
		app.Logger().Warn("webhook_delivery_logs collection not found", slog.Any("error", err))
		return result
	}

	record := core.NewRecord(col)
	record.Set("webhook", webhook.Id)
	record.Set("event", event)
	record.Set("url", url)
	record.Set("request_body", redactPII(payload))
	record.Set("response_status", responseStatus)
	record.Set("response_body", responseBody)
	record.Set("delivery_status", deliveryStatus)
	record.Set("error_message", errMsg)
	record.Set("duration_ms", durationMs)

	// Schedule first retry for initial failed deliveries.
	// Manual test deliveries are never auto-retried.
	if deliveryStatus == "failed" && event != "test" {
		record.Set("retry_count", 0)
		nextRetry := time.Now().UTC().Add(webhookRetryBackoffs[0])
		record.Set("next_retry_at", nextRetry.Format(time.RFC3339))
	}

	if err := app.Save(record); err != nil {
		app.Logger().Warn("failed to save webhook delivery log", slog.Any("error", err))
	} else {
		result.LogRecord = record
	}

	return result
}

// VerifyWebhookSignature verifies an HMAC-SHA256 signature.
func VerifyWebhookSignature(secretKey, payload, signature string) bool {
	expected := generateHMAC(secretKey, payload)
	return hmac.Equal([]byte(expected), []byte(signature))
}

func generateHMAC(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// marshalSorted produces deterministic JSON with sorted keys.
func marshalSorted(m map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.Grow(128)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(k)
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valJSON, _ := json.Marshal(m[k])
		// Remove trailing whitespace for compact output
		buf.Write(bytes.TrimRight(valJSON, " "))
	}
	buf.WriteByte('}')

	// Verify it's valid JSON
	var check map[string]any
	if err := json.Unmarshal(buf.Bytes(), &check); err != nil {
		// Fall back to standard marshal
		return json.Marshal(m)
	}

	return buf.Bytes(), nil
}

// redactPII returns a copy of the payload with sensitive fields masked for storage in logs.
func redactPII(payload map[string]any) map[string]any {
	redacted := make(map[string]any, len(payload))
	for k, v := range payload {
		redacted[k] = v
	}
	if _, ok := redacted["body"]; ok {
		redacted["body"] = "[redacted]"
	}
	if v, ok := redacted["from"].(string); ok {
		redacted["from"] = maskPhone(v)
	}
	if v, ok := redacted["to"].(string); ok {
		redacted["to"] = maskPhone(v)
	}
	return redacted
}

// maskPhone replaces middle digits: +1234567890 → +1****7890
func maskPhone(phone string) string {
	if len(phone) <= 6 {
		return phone
	}
	return phone[:2] + strings.Repeat("*", len(phone)-6) + phone[len(phone)-4:]
}

var webhookBreaker = NewHostCircuitBreaker("webhook", 5, 60*time.Second, 5*time.Minute)

// webhookUnparseableHostBucket groups deliveries to URLs whose host cannot
// be extracted, so the circuit-breaker still gates them rather than
// silently bypassing protection.
const webhookUnparseableHostBucket = "_unparseable_"

// webhookHost returns the hostname (lowercased) used as the per-host
// circuit-breaker key. Same logical destination expressed in different
// case must map to one breaker, otherwise failures split across buckets
// and the breaker trips slower than maxFails implies.
//
// Unparseable URLs or URLs without a host fall back to a shared sentinel
// bucket so the breaker still applies — degraded gating, not bypass.
func webhookHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return webhookUnparseableHostBucket
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return webhookUnparseableHostBucket
	}
	return host
}

// TriggerWebhooks finds active webhook configs for a user and fires matching webhooks.
// Deliveries run in a single background goroutine over a freshly fetched copy
// of the message: core.Record is not safe for concurrent mutation, and the
// caller may still hold (or save) its own instance.
func TriggerWebhooks(app core.App, userId string, message *core.Record, event string) {
	webhooks, err := app.FindRecordsByFilter(
		"webhook_configs",
		"user = {:userId} && active = true",
		"", 0, 0,
		dbx.Params{"userId": userId},
	)
	if err != nil || len(webhooks) == 0 {
		return
	}

	matching := make([]*core.Record, 0, len(webhooks))
	for _, wh := range webhooks {
		if containsEvent(wh.GetString("events"), event) {
			matching = append(matching, wh)
		}
	}
	if len(matching) == 0 {
		return
	}

	messageId := message.Id
	routine.FireAndForget(func() {
		// Re-fetch so this goroutine owns its record exclusively. Callers
		// save the message before triggering, so the fresh copy is current.
		msg, err := app.FindRecordById("sms_messages", messageId)
		if err != nil {
			app.Logger().Warn("webhook trigger: message not found",
				slog.String("message", messageId), slog.Any("error", err))
			return
		}

		anySuccess := false
		for _, wh := range matching {
			host := webhookHost(wh.GetString("url"))
			if !webhookBreaker.Allow(host) {
				app.Logger().Debug("webhook circuit breaker open, skipping delivery",
					slog.String("event", event), slog.String("host", host))
				continue
			}
			if err := SendWebhookForMessage(app, wh, msg, event); err != nil {
				webhookBreaker.RecordFailure(host)
				app.Logger().Warn("webhook delivery failed", slog.Any("error", err))
			} else {
				webhookBreaker.RecordSuccess(host)
				anySuccess = true
			}
		}

		// Mark webhook_sent once after the fan-out instead of per delivery.
		if anySuccess {
			msg.Set("webhook_sent", true)
			if err := app.Save(msg); err != nil {
				app.Logger().Warn("failed to update webhook_sent", slog.Any("error", err))
			}
		}
	})
}

// containsEvent checks if a JSON array string contains a specific event.
func containsEvent(eventsJSON string, event string) bool {
	if eventsJSON == "" {
		return false
	}
	var events []string
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
		return strings.Contains(eventsJSON, event)
	}
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}

// parseStoredPayload extracts a map from a stored request_body value,
// handling the multiple types PocketBase may return (map, string, or raw JSON).
func parseStoredPayload(raw any) (map[string]any, error) {
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case string:
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return nil, fmt.Errorf("parse stored payload: %w", err)
		}
		return m, nil
	default:
		rawJSON, _ := json.Marshal(raw)
		var m map[string]any
		if err := json.Unmarshal(rawJSON, &m); err != nil {
			return nil, fmt.Errorf("parse stored payload: %w", err)
		}
		return m, nil
	}
}

// rebuildPayloadFromLog reconstructs the real delivery payload for a retry.
// The stored request_body is PII-redacted for display (body "[redacted]",
// masked phone numbers) and must never be re-delivered, so the payload is
// regenerated from the original message record instead.
func rebuildPayloadFromLog(app core.App, webhook, logRecord *core.Record) (map[string]any, error) {
	stored, err := parseStoredPayload(logRecord.Get("request_body"))
	if err != nil {
		return nil, err
	}

	messageId, _ := stored["message_id"].(string)
	if messageId == "" {
		return nil, fmt.Errorf("stored payload has no message_id")
	}

	message, err := app.FindRecordById("sms_messages", messageId)
	if err != nil {
		return nil, fmt.Errorf("message %s no longer exists: %w", messageId, err)
	}

	return buildMessagePayload(webhook, message, logRecord.GetString("event")), nil
}

// RetryFailedWebhooks retries failed webhook deliveries with exponential backoff.
// Each retry updates the existing delivery log in place — it must NOT create
// new failed log records, or every failure would re-enter the retry queue and
// amplify deliveries exponentially.
func RetryFailedWebhooks(app core.App) error {
	now := FilterNow()

	records, err := app.FindRecordsByFilter(
		"webhook_delivery_logs",
		"delivery_status = 'failed' && next_retry_at != '' && next_retry_at <= {:now} && retry_count < {:maxRetries}",
		"-created", 50, 0,
		dbx.Params{"now": now, "maxRetries": WebhookMaxRetries},
	)
	if err != nil {
		return err
	}

	retried := 0
	for _, record := range records {
		webhookId := record.GetString("webhook")
		webhook, err := app.FindRecordById("webhook_configs", webhookId)
		if err != nil {
			app.Logger().Warn("webhook config not found for retry", slog.String("webhook", webhookId))
			// Clear next_retry_at so we don't keep trying
			record.Set("next_retry_at", "")
			_ = app.Save(record)
			continue
		}

		if !webhook.GetBool("active") {
			record.Set("next_retry_at", "")
			_ = app.Save(record)
			continue
		}

		host := webhookHost(webhook.GetString("url"))
		if !webhookBreaker.Allow(host) {
			app.Logger().Debug("webhook circuit breaker open, skipping retry",
				slog.String("host", host))
			continue
		}

		payload, err := rebuildPayloadFromLog(app, webhook, record)
		if err != nil {
			app.Logger().Warn("cannot rebuild webhook payload for retry", slog.Any("error", err))
			record.Set("next_retry_at", "")
			_ = app.Save(record)
			continue
		}

		result := executeWebhookRequest(app, webhook, payload)

		retryCount := record.GetInt("retry_count") + 1
		record.Set("retry_count", retryCount)

		if result.DeliveryStatus == "success" {
			record.Set("delivery_status", "success")
			record.Set("response_status", result.ResponseStatus)
			record.Set("next_retry_at", "")
			record.Set("error_message", "")
			record.Set("duration_ms", result.DurationMs)
			webhookBreaker.RecordSuccess(host)
		} else {
			record.Set("error_message", result.ErrorMessage)
			record.Set("duration_ms", result.DurationMs)
			if result.ResponseStatus > 0 {
				record.Set("response_status", result.ResponseStatus)
			}
			if retryCount < WebhookMaxRetries {
				nextRetry := time.Now().UTC().Add(webhookRetryBackoffs[retryCount])
				record.Set("next_retry_at", nextRetry.Format(time.RFC3339))
			} else {
				record.Set("next_retry_at", "")
			}
			webhookBreaker.RecordFailure(host)
		}

		if err := app.Save(record); err != nil {
			app.Logger().Warn("failed to update webhook retry log", slog.Any("error", err))
		}
		retried++
	}

	if retried > 0 {
		app.Logger().Info("Retried failed webhooks", slog.Int("retried", retried))
	}
	return nil
}

// RetryWebhookDelivery manually retries a single failed webhook delivery.
func RetryWebhookDelivery(app core.App, logId string) (*WebhookDeliveryResult, error) {
	record, err := app.FindRecordById("webhook_delivery_logs", logId)
	if err != nil {
		return nil, fmt.Errorf("delivery log not found")
	}

	if record.GetString("delivery_status") != "failed" {
		return nil, fmt.Errorf("can only retry failed deliveries")
	}

	webhookId := record.GetString("webhook")
	webhook, err := app.FindRecordById("webhook_configs", webhookId)
	if err != nil {
		return nil, fmt.Errorf("webhook config not found")
	}

	// Regenerate the payload from the original message — the stored
	// request_body is PII-redacted and must never be re-delivered.
	payload, err := rebuildPayloadFromLog(app, webhook, record)
	if err != nil {
		return nil, fmt.Errorf("cannot rebuild webhook payload: %v", err)
	}

	event := record.GetString("event")
	result := deliverWebhook(app, webhook, payload, event)

	// Link the new log entry to the original
	if result.LogRecord != nil {
		result.LogRecord.Set("original_log", logId)
		_ = app.Save(result.LogRecord)
	}

	// Clear auto-retry on the original to prevent duplicate retries
	record.Set("next_retry_at", "")
	_ = app.Save(record)

	return result, nil
}

