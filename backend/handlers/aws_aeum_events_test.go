package handlers

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"vendel/services/smsprovider"
)

// aeumTestEnv carries the per-test signing key, cert server, and the cert
// URL used by every SNS payload built in this file. SNS override restoration
// is registered with t.Cleanup; only the test server still needs an explicit
// Close().
type aeumTestEnv struct {
	key        *rsa.PrivateKey
	certServer *httptest.Server
	certURL    string
}

// aeumDefaultTopicARN is the topic ARN baked into every SNS payload signed by
// signedNotification / signedSubscriptionConfirmation. newAEUMTestEnv sets
// AEUM_SNS_TOPIC_ARN to this value so the handler's allowlist accepts the
// payloads by default; tests that need a mismatch override via t.Setenv.
const aeumDefaultTopicARN = "arn:aws:sns:us-east-1:111122223333:vendel-sms-events"

func newAEUMTestEnv(t testing.TB) *aeumTestEnv {
	t.Helper()
	// Make every test pass the fail-closed topic check by default. Tests that
	// want to exercise the mismatch or missing-config paths override via
	// t.Setenv after newAEUMTestEnv returns.
	t.Setenv("AEUM_SNS_TOPIC_ARN", aeumDefaultTopicARN)

	key, certPEM := aeumGenerateKeyCertPEM(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(certPEM)
	}))

	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatalf("parse server URL: %v", err)
	}

	restore := smsprovider.InstallSNSTestOverrides(
		server.Client(),
		regexp.MustCompile("^"+regexp.QuoteMeta(parsed.Host)+"$"),
	)
	t.Cleanup(restore)

	return &aeumTestEnv{
		key:        key,
		certServer: server,
		certURL:    server.URL + "/cert.pem",
	}
}

func (env *aeumTestEnv) Close() {
	env.certServer.Close()
}

// aeumGenerateKeyCertPEM produces a fresh RSA key + self-signed cert.
func aeumGenerateKeyCertPEM(t testing.TB) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// signedNotification renders an SNS Notification payload signed with env.key.
func (env *aeumTestEnv) signedNotification(t testing.TB, eventPayload string, timestamp time.Time) string {
	t.Helper()
	ts := timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
	msg := smsprovider.SNSMessage{
		Type:             "Notification",
		MessageID:        "sns-msg-" + ts,
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          eventPayload,
		Timestamp:        ts,
		SignatureVersion: "2",
		SigningCertURL:   env.certURL,
	}
	env.sign(t, &msg)
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal SNS msg: %v", err)
	}
	return string(out)
}

// signedSubscriptionConfirmation builds and signs a SubscriptionConfirmation
// envelope. SubscribeURL points to a stub server controlled by the caller.
func (env *aeumTestEnv) signedSubscriptionConfirmation(t testing.TB, subscribeURL string) string {
	t.Helper()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	msg := smsprovider.SNSMessage{
		Type:             "SubscriptionConfirmation",
		MessageID:        "sns-sub-1",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          "You have chosen to subscribe.",
		Token:            "tok-abc",
		SubscribeURL:     subscribeURL,
		Timestamp:        ts,
		SignatureVersion: "2",
		SigningCertURL:   env.certURL,
	}
	env.sign(t, &msg)
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal SNS msg: %v", err)
	}
	return string(out)
}

// signedNotificationWithBadSignature builds a notification whose payload was
// signed correctly, then mutates Message after signing so the signature no
// longer verifies.
func (env *aeumTestEnv) signedNotificationWithBadSignature(t testing.TB, eventPayload string) string {
	t.Helper()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	msg := smsprovider.SNSMessage{
		Type:             "Notification",
		MessageID:        "sns-bad-1",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          eventPayload,
		Timestamp:        ts,
		SignatureVersion: "2",
		SigningCertURL:   env.certURL,
	}
	env.sign(t, &msg)
	// Tamper the message body so the signature no longer matches.
	msg.Message = eventPayload + " tampered"
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal SNS msg: %v", err)
	}
	return string(out)
}

func (env *aeumTestEnv) sign(t testing.TB, msg *smsprovider.SNSMessage) {
	t.Helper()
	canonical, err := smsprovider.BuildCanonical(msg)
	if err != nil {
		t.Fatalf("buildCanonicalString: %v", err)
	}
	sum := sha256.Sum256([]byte(canonical))
	sig, err := rsa.SignPKCS1v15(rand.Reader, env.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	msg.Signature = base64.StdEncoding.EncodeToString(sig)
}

// setupAEUMTestApp builds a TestApp wired with only the AEUM event route. We
// deliberately skip the encryption hooks: the row we pre-load has no body, and
// the handler does not mutate body.
func setupAEUMTestApp(t testing.TB) *tests.TestApp {
	testApp, err := tests.NewTestApp(testDataDir)
	if err != nil {
		t.Fatal(err)
	}
	testApp.OnServe().BindFunc(func(se *core.ServeEvent) error {
		RegisterAEUMEventRoutes(se)
		return se.Next()
	})
	return testApp
}

// seedMessageWithProviderID creates an sms_messages row tied to the seed user
// + device, with status="sent" and a known provider_message_id. Returns the
// record id.
func seedMessageWithProviderID(t testing.TB, app core.App, providerID string) string {
	t.Helper()
	user, err := app.FindFirstRecordByFilter("users", "email = 'user@test.com'")
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	device, err := app.FindFirstRecordByFilter("sms_devices", "user = {:uid}", map[string]any{"uid": user.Id})
	if err != nil {
		t.Fatalf("find device: %v", err)
	}
	collection, err := app.FindCollectionByNameOrId("sms_messages")
	if err != nil {
		t.Fatalf("find collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("to", "+15551234567")
	record.Set("body", "ignored-test-body")
	record.Set("status", "sent")
	record.Set("message_type", "outgoing")
	record.Set("user", user.Id)
	record.Set("device", device.Id)
	record.Set("provider_message_id", providerID)
	record.Set("provider_channel", "auto")
	if err := app.Save(record); err != nil {
		t.Fatalf("save msg: %v", err)
	}
	return record.Id
}

// aeumEventJSON marshals the AEUM-specific inner event JSON. The
// originationPhoneNumber arg is optional; pass "" to omit it (mirrors AEUM
// payloads where the field may not be populated yet).
func aeumEventJSON(eventType, providerMessageID string, eventTimeMs int64, originationPhoneNumber string) string {
	m := map[string]any{
		"eventType":      eventType,
		"messageId":      providerMessageID,
		"eventTimestamp": eventTimeMs,
	}
	if originationPhoneNumber != "" {
		m["originationPhoneNumber"] = originationPhoneNumber
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestAEUMEvent_InvalidSignature(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	body := env.signedNotificationWithBadSignature(t, `{"eventType":"TEXT_DELIVERED","messageId":"any"}`)

	scenario := tests.ApiScenario{
		Name:            "invalid SNS signature is rejected",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  401,
		ExpectedContent: []string{`"message"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_MalformedJSON(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "malformed JSON returns 400",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(`{not json`),
		ExpectedStatus:  400,
		ExpectedContent: []string{`"message"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_UnknownType(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	// Build a valid signature for a custom Type ("Garbage"). Because
	// buildCanonicalString only knows Notification / SubscriptionConfirmation
	// / UnsubscribeConfirmation, signature verification will surface an
	// error before the Type switch sees it, so we expect 401, not 400.
	//
	// Use a Notification envelope but flip the Type at the very end to
	// trigger the "unsupported SNS Type" branch on the verifier.
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	msg := smsprovider.SNSMessage{
		Type:             "Garbage",
		MessageID:        "id",
		TopicARN:         "arn",
		Message:          "x",
		Timestamp:        ts,
		SignatureVersion: "2",
		Signature:        base64.StdEncoding.EncodeToString([]byte("ignored")),
		SigningCertURL:   env.certURL,
	}
	b, _ := json.Marshal(msg)

	scenario := tests.ApiScenario{
		Name:            "unknown Type is rejected by signature verification",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(string(b)),
		ExpectedStatus:  401,
		ExpectedContent: []string{`"message"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_SubscriptionConfirmation(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	// ConfirmSNSSubscription now refuses non-AWS-SNS hosts, so the SubscribeURL
	// must resolve to the same TLS cert server (whose host the env installs into
	// awsSNSHostRe and whose client trusts its cert). It replies 200 on any
	// path, which is all the confirmation GET checks.
	body := env.signedSubscriptionConfirmation(t, env.certServer.URL+"/?Action=ConfirmSubscription&Token=tok-abc")

	scenario := tests.ApiScenario{
		Name:            "SubscriptionConfirmation confirms and returns 200",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"status":"confirmed"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_OrphanRecent(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	// Build an event whose messageId does NOT match any sms_messages row;
	// eventTimestamp is "now" so the freshness window applies → 503.
	nowMs := time.Now().UnixMilli()
	inner := aeumEventJSON("TEXT_DELIVERED", "missing-provider-id", nowMs, "")
	body := env.signedNotification(t, inner, time.Now())

	scenario := tests.ApiScenario{
		Name:            "orphan event in fresh window returns 503",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  503,
		ExpectedContent: []string{`"message"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_OrphanStale(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	// Event timestamp 10 minutes in the past → past freshness window → 200.
	staleTime := time.Now().Add(-10 * time.Minute)
	staleMs := staleTime.UnixMilli()
	inner := aeumEventJSON("TEXT_DELIVERED", "missing-provider-id", staleMs, "")
	body := env.signedNotification(t, inner, staleTime)

	scenario := tests.ApiScenario{
		Name:            "orphan event past freshness window returns 200",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"orphan"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_DeliveredHappyPath(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	const providerID = "aws-provider-msg-delivered-1"
	const originationPhone = "+15550009999"

	inner := aeumEventJSON("TEXT_DELIVERED", providerID, time.Now().UnixMilli(), originationPhone)
	body := env.signedNotification(t, inner, time.Now())

	var msgID string
	var afterCheckErr error
	scenario := tests.ApiScenario{
		Name:            "TEXT_DELIVERED updates row to delivered",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"status":"ok"`},
		Delay:           50 * time.Millisecond,
		TestAppFactory:  setupAEUMTestApp,
		BeforeTestFunc: func(t testing.TB, ta *tests.TestApp, se *core.ServeEvent) {
			msgID = seedMessageWithProviderID(t, ta, providerID)
		},
		AfterTestFunc: func(t testing.TB, ta *tests.TestApp, res *http.Response) {
			rec, err := ta.FindRecordById("sms_messages", msgID)
			if err != nil {
				afterCheckErr = fmt.Errorf("find updated record: %w", err)
				return
			}
			if got := rec.GetString("status"); got != "delivered" {
				afterCheckErr = fmt.Errorf("status = %q, want delivered", got)
				return
			}
			if got := rec.GetString("provider_channel"); got != "sms" {
				afterCheckErr = fmt.Errorf("provider_channel = %q, want sms (derived from TEXT_* prefix)", got)
				return
			}
			if got := rec.GetString("provider_origination_identity"); got != originationPhone {
				afterCheckErr = fmt.Errorf("provider_origination_identity = %q, want %q", got, originationPhone)
				return
			}
			if rec.GetDateTime("delivered_at").IsZero() {
				afterCheckErr = fmt.Errorf("delivered_at not set")
			}
		},
	}
	scenario.Test(t)
	if afterCheckErr != nil {
		t.Fatal(afterCheckErr)
	}
}

func TestAEUMEvent_FailedEvent(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	const providerID = "aws-provider-msg-failed-1"

	inner := aeumEventJSON("TEXT_BLOCKED", providerID, time.Now().UnixMilli(), "")
	body := env.signedNotification(t, inner, time.Now())

	var msgID string
	var afterCheckErr error
	scenario := tests.ApiScenario{
		Name:            "TEXT_BLOCKED updates row to failed",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"status":"ok"`},
		Delay:           50 * time.Millisecond,
		TestAppFactory:  setupAEUMTestApp,
		BeforeTestFunc: func(t testing.TB, ta *tests.TestApp, se *core.ServeEvent) {
			msgID = seedMessageWithProviderID(t, ta, providerID)
		},
		AfterTestFunc: func(t testing.TB, ta *tests.TestApp, res *http.Response) {
			rec, err := ta.FindRecordById("sms_messages", msgID)
			if err != nil {
				afterCheckErr = fmt.Errorf("find updated record: %w", err)
				return
			}
			if got := rec.GetString("status"); got != "failed" {
				afterCheckErr = fmt.Errorf("status = %q, want failed", got)
			}
		},
	}
	scenario.Test(t)
	if afterCheckErr != nil {
		t.Fatal(afterCheckErr)
	}
}

func TestAEUMEvent_IgnoredEvent(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	const providerID = "aws-provider-msg-ignored-1"

	inner := aeumEventJSON("TEXT_QUEUED", providerID, time.Now().UnixMilli(), "")
	body := env.signedNotification(t, inner, time.Now())

	var msgID string
	var afterCheckErr error
	scenario := tests.ApiScenario{
		Name:            "TEXT_QUEUED is ignored without status change",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"ignored"`},
		TestAppFactory:  setupAEUMTestApp,
		BeforeTestFunc: func(t testing.TB, ta *tests.TestApp, se *core.ServeEvent) {
			msgID = seedMessageWithProviderID(t, ta, providerID)
		},
		AfterTestFunc: func(t testing.TB, ta *tests.TestApp, res *http.Response) {
			rec, err := ta.FindRecordById("sms_messages", msgID)
			if err != nil {
				afterCheckErr = fmt.Errorf("find updated record: %w", err)
				return
			}
			// Transient events must not modify the row.
			if got := rec.GetString("status"); got != "sent" {
				afterCheckErr = fmt.Errorf("status = %q, want sent (unchanged)", got)
			}
		},
	}
	scenario.Test(t)
	if afterCheckErr != nil {
		t.Fatal(afterCheckErr)
	}
}

func TestAEUMEvent_TopicARNMissingFailsClosed(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	// Override the default set by newAEUMTestEnv so the handler sees an
	// empty allowlist and must fail closed.
	t.Setenv("AEUM_SNS_TOPIC_ARN", "")

	const providerID = "aws-provider-msg-topic-missing"
	inner := aeumEventJSON("TEXT_DELIVERED", providerID, time.Now().UnixMilli(), "")
	body := env.signedNotification(t, inner, time.Now())

	scenario := tests.ApiScenario{
		Name:            "missing AEUM_SNS_TOPIC_ARN refuses delivery (fail-closed)",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  500,
		ExpectedContent: []string{`"message"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_DeliveredRCSChannel(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	const providerID = "aws-provider-msg-rcs-1"
	const rcsAgent = "arn:aws:sms-voice:us-east-1:111122223333:agent/MyBrandRCS"

	// AEUM emits TEXT_SUCCESSFUL for RCS deliveries too; the channel must
	// come from the shape of originationPhoneNumber, not the eventType.
	inner := aeumEventJSON("TEXT_SUCCESSFUL", providerID, time.Now().UnixMilli(), rcsAgent)
	body := env.signedNotification(t, inner, time.Now())

	var msgID string
	var afterCheckErr error
	scenario := tests.ApiScenario{
		Name:            "RCS-shaped originationPhoneNumber sets provider_channel=rcs",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"status":"ok"`},
		Delay:           50 * time.Millisecond,
		TestAppFactory:  setupAEUMTestApp,
		BeforeTestFunc: func(t testing.TB, ta *tests.TestApp, se *core.ServeEvent) {
			msgID = seedMessageWithProviderID(t, ta, providerID)
		},
		AfterTestFunc: func(t testing.TB, ta *tests.TestApp, res *http.Response) {
			rec, err := ta.FindRecordById("sms_messages", msgID)
			if err != nil {
				afterCheckErr = fmt.Errorf("find updated record: %w", err)
				return
			}
			if got := rec.GetString("status"); got != "delivered" {
				afterCheckErr = fmt.Errorf("status = %q, want delivered", got)
				return
			}
			if got := rec.GetString("provider_channel"); got != "rcs" {
				afterCheckErr = fmt.Errorf("provider_channel = %q, want rcs (derived from non-E.164 identity)", got)
				return
			}
			if got := rec.GetString("provider_origination_identity"); got != rcsAgent {
				afterCheckErr = fmt.Errorf("provider_origination_identity = %q, want %q", got, rcsAgent)
			}
		},
	}
	scenario.Test(t)
	if afterCheckErr != nil {
		t.Fatal(afterCheckErr)
	}
}

func TestAEUMEvent_TopicARNMismatchRejected(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	t.Setenv("AEUM_SNS_TOPIC_ARN", "arn:aws:sns:us-east-1:000000000000:other-topic")

	const providerID = "aws-provider-msg-topic-mismatch"
	inner := aeumEventJSON("TEXT_DELIVERED", providerID, time.Now().UnixMilli(), "")
	body := env.signedNotification(t, inner, time.Now())

	scenario := tests.ApiScenario{
		Name:            "SNS message from unexpected topic is rejected",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  403,
		ExpectedContent: []string{`"message"`},
		TestAppFactory:  setupAEUMTestApp,
	}
	scenario.Test(t)
}

func TestAEUMEvent_TopicARNMatchAccepted(t *testing.T) {
	env := newAEUMTestEnv(t)
	defer env.Close()

	// signedNotification uses TopicARN "arn:aws:sns:us-east-1:111122223333:vendel-sms-events".
	t.Setenv("AEUM_SNS_TOPIC_ARN", "arn:aws:sns:us-east-1:111122223333:vendel-sms-events")

	const providerID = "aws-provider-msg-topic-match"
	inner := aeumEventJSON("TEXT_DELIVERED", providerID, time.Now().UnixMilli(), "")
	body := env.signedNotification(t, inner, time.Now())

	scenario := tests.ApiScenario{
		Name:            "SNS message from expected topic is accepted",
		Method:          http.MethodPost,
		URL:             "/api/webhooks/aws-aeum-events",
		Body:            strings.NewReader(body),
		ExpectedStatus:  200,
		ExpectedContent: []string{`"status":"ok"`},
		Delay:           50 * time.Millisecond,
		TestAppFactory:  setupAEUMTestApp,
		BeforeTestFunc: func(t testing.TB, ta *tests.TestApp, se *core.ServeEvent) {
			seedMessageWithProviderID(t, ta, providerID)
		},
	}
	scenario.Test(t)
}

func TestDeriveProviderChannel(t *testing.T) {
	cases := []struct {
		name     string
		identity string
		want     string
	}{
		{"E164_long_code", "+15551234567", "sms"},
		{"E164_short_with_plus", "+1234", "sms"},
		{"E164_with_spaces_stripped", "  +15551234567  ", "sms"},
		{"bare_short_code", "12345", "sms"},
		{"bare_short_code_three_digit", "611", "sms"},
		{"bare_numeric_with_spaces_stripped", "  12345  ", "sms"},
		{"RCS_agent_name", "MyBrandRCS", "rcs"},
		{"RCS_agent_arn", "arn:aws:sms-voice:us-east-1:111122223333:agent/abc", "rcs"},
		{"empty", "", ""},
		{"whitespace_only", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveProviderChannel(tc.identity); got != tc.want {
				t.Errorf("deriveProviderChannel(%q) = %q, want %q", tc.identity, got, tc.want)
			}
		})
	}
}

func TestMapAEUMEventType(t *testing.T) {
	cases := []struct {
		eventType string
		want      string
	}{
		{"TEXT_DELIVERED", "delivered"},
		{"TEXT_SUCCESSFUL", "delivered"},
		{"TEXT_TTL_EXPIRED", "failed"},
		{"TEXT_BLOCKED", "failed"},
		{"TEXT_CARRIER_UNREACHABLE", "failed"},
		{"TEXT_INVALID", "failed"},
		{"TEXT_INVALID_MESSAGE", "failed"},
		{"TEXT_UNKNOWN", "failed"},
		{"TEXT_UNREACHABLE", "failed"},
		{"TEXT_CARRIER_BLOCKED", "failed"},
		{"TEXT_SPAM", "failed"},
		{"TEXT_PROTECT_BLOCKED", "failed"},
		{"TEXT_PENDING", ""},
		{"TEXT_QUEUED", ""},
		{"TEXT_SENT", ""},
		{"UNHANDLED_EVENT", ""},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			if got := mapAEUMEventType(tc.eventType); got != tc.want {
				t.Errorf("mapAEUMEventType(%q) = %q, want %q", tc.eventType, got, tc.want)
			}
		})
	}
}
