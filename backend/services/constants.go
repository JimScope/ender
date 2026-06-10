package services

import "time"

// ── Application ─────────────────────────────────────────────────────

const DefaultAppName = "Vendel"

// ── SMS ─────────────────────────────────────────────────────────────

const MaxMessageBodyLength = 1600

// ── API Keys & Device Keys ──────────────────────────────────────────

const (
	APIKeyPrefix     = "vk_"
	DeviceKeyPrefix  = "dk_"
	GeneratedKeyLen  = 32
	KeyPrefixDisplay = 10 // characters shown in "vk_XXXXXX..."
)

// ── Auth ────────────────────────────────────────────────────────────

const (
	MinPasswordLength      = 10
	AuthTokenDurationSecs  = 86400 // 24 hours
	DefaultAPIKeyExpiryYrs = 1 // years from creation
)

// ── Webhook Delivery ────────────────────────────────────────────────

const (
	WebhookMaxRetries       = 3
	WebhookDialTimeout      = 10 * time.Second
	WebhookDefaultTimeout   = 10 // seconds (per-webhook configurable)
	WebhookMaxTimeout       = 30 // seconds (hard cap)
	WebhookMaxRedirects     = 3
	WebhookResponseMaxBytes = 2048
	WebhookResponseMaxChars = 2000
	WebhookIdleConnTimeout  = 90 * time.Second
	WebhookMaxIdleConns     = 20
	WebhookMaxIdlePerHost   = 5
)

// ── Webhook Retry Backoffs ──────────────────────────────────────────

var WebhookRetryBackoffs = []time.Duration{
	1 * time.Minute,  // after 1st failure
	5 * time.Minute,  // after 2nd failure
	15 * time.Minute, // after 3rd failure
}

// ── SMS Retry ───────────────────────────────────────────────────────

const (
	SMSMaxRetries  = 3
	SMSRetryCutoff = 24 * time.Hour // only retry messages younger than this

	// SMSSendingStaleAfter marks "sending" messages as stuck: an agent claims
	// them via /api/sms/pending and a healthy send completes in well under a
	// minute, so anything still "sending" after this window belongs to an
	// agent that died mid-batch and must be re-queued.
	SMSSendingStaleAfter = 10 * time.Minute
)

var SMSRetryBackoffs = []time.Duration{
	15 * time.Minute, // after 1st failure
	1 * time.Hour,    // after 2nd failure
	6 * time.Hour,    // after 3rd failure
}

// ── External Service Timeouts ───────────────────────────────────────

const (
	PaymentClientTimeout = 30 * time.Second
	FCMContextTimeout    = 10 * time.Second
)
