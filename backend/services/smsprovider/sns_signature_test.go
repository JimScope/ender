package smsprovider

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"
)

// generateTestKeyAndCertPEM builds an in-memory RSA key + self-signed cert so
// the tests can sign synthetic SNS payloads without ever hitting AWS.
func generateTestKeyAndCertPEM(t *testing.T) (*rsa.PrivateKey, []byte) {
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

// mustCompileHostRe builds a regex that matches a single literal host. Used
// to swap the package-level awsSNSHostRe so tests can point fetchCertPublicKey
// at an httptest server (127.0.0.1:NNNNN) instead of the real AWS host list.
func mustCompileHostRe(host string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(host) + "$")
}

// signSNSMessage canonicalises msg, signs it with key using RSA-SHA256
// (matching SignatureVersion="2"), and stores the base64-encoded signature
// back into the message.
func signSNSMessage(t *testing.T, key *rsa.PrivateKey, msg *SNSMessage) {
	t.Helper()
	canonical, err := buildCanonicalString(msg)
	if err != nil {
		t.Fatalf("buildCanonicalString: %v", err)
	}
	sum := sha256.Sum256([]byte(canonical))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	msg.Signature = base64.StdEncoding.EncodeToString(sig)
}

// startCertServer spins up an httptest TLS server that serves certPEM at the
// /cert.pem path. It returns the server, the cert URL, and a cleanup function
// that restores the package-level httpClient and awsSNSHostRe overrides.
func startCertServer(t *testing.T, certPEM []byte) (*httptest.Server, string, func()) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(certPEM)
	}))

	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatalf("parse server URL: %v", err)
	}

	restore := InstallSNSTestOverrides(server.Client(), mustCompileHostRe(parsed.Host))
	t.Cleanup(restore)
	t.Cleanup(server.Close)

	certURL := server.URL + "/cert.pem"
	return server, certURL, func() {}
}

func TestVerifySNSSignature_ValidNotification(t *testing.T) {
	key, certPEM := generateTestKeyAndCertPEM(t)
	_, certURL, cleanup := startCertServer(t, certPEM)
	defer cleanup()

	msg := &SNSMessage{
		Type:             "Notification",
		MessageID:        "msg-1",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          "hello",
		Timestamp:        "2026-01-01T00:00:00.000Z",
		SignatureVersion: "2",
		SigningCertURL:   certURL,
	}
	signSNSMessage(t, key, msg)

	if err := VerifySNSSignature(msg); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestVerifySNSSignature_TamperedMessage(t *testing.T) {
	key, certPEM := generateTestKeyAndCertPEM(t)
	_, certURL, cleanup := startCertServer(t, certPEM)
	defer cleanup()

	msg := &SNSMessage{
		Type:             "Notification",
		MessageID:        "msg-2",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          "original",
		Timestamp:        "2026-01-01T00:00:00.000Z",
		SignatureVersion: "2",
		SigningCertURL:   certURL,
	}
	signSNSMessage(t, key, msg)

	// Mutate the payload after signing — verification must fail.
	msg.Message = "tampered"
	if err := VerifySNSSignature(msg); err == nil {
		t.Fatal("expected verification to fail for tampered message, got nil")
	}
}

func TestVerifySNSSignature_ValidSubscriptionConfirmation(t *testing.T) {
	key, certPEM := generateTestKeyAndCertPEM(t)
	_, certURL, cleanup := startCertServer(t, certPEM)
	defer cleanup()

	msg := &SNSMessage{
		Type:             "SubscriptionConfirmation",
		MessageID:        "sub-1",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          "You have chosen to subscribe to the topic",
		Token:            "tok-abcdef",
		SubscribeURL:     "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&Token=tok-abcdef",
		Timestamp:        "2026-01-01T00:00:00.000Z",
		SignatureVersion: "2",
		SigningCertURL:   certURL,
	}
	signSNSMessage(t, key, msg)

	if err := VerifySNSSignature(msg); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestVerifySNSSignature_NonAWSHost(t *testing.T) {
	msg := &SNSMessage{
		Type:             "Notification",
		MessageID:        "msg-3",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          "hi",
		Timestamp:        "2026-01-01T00:00:00.000Z",
		SignatureVersion: "2",
		Signature:        base64.StdEncoding.EncodeToString([]byte("fake")),
		SigningCertURL:   "https://attacker.example.com/cert.pem",
	}
	if err := VerifySNSSignature(msg); err == nil {
		t.Fatal("expected error for non-AWS SigningCertURL host, got nil")
	}
}

func TestVerifySNSSignature_NonHTTPSCertURL(t *testing.T) {
	msg := &SNSMessage{
		Type:             "Notification",
		MessageID:        "msg-4",
		TopicARN:         "arn:aws:sns:us-east-1:111122223333:vendel-sms-events",
		Message:          "hi",
		Timestamp:        "2026-01-01T00:00:00.000Z",
		SignatureVersion: "2",
		Signature:        base64.StdEncoding.EncodeToString([]byte("fake")),
		SigningCertURL:   "http://sns.us-east-1.amazonaws.com/cert.pem",
	}
	if err := VerifySNSSignature(msg); err == nil {
		t.Fatal("expected error for non-HTTPS SigningCertURL, got nil")
	}
}

func TestValidateCertURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid US region", "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-abc.pem", false},
		{"valid China region", "https://sns.cn-north-1.amazonaws.com.cn/SimpleNotificationService-abc.pem", false},
		{"missing pem suffix", "https://sns.us-east-1.amazonaws.com/cert", true},
		{"http scheme", "http://sns.us-east-1.amazonaws.com/cert.pem", true},
		{"foreign host", "https://evil.example.com/cert.pem", true},
		{"close lookalike", "https://sns.us-east-1.amazonaws.com.evil.com/cert.pem", true},
		{"bad url", "::not-a-url", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCertURL(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildCanonicalString_UnsupportedType(t *testing.T) {
	if _, err := buildCanonicalString(&SNSMessage{Type: "WeirdType"}); err == nil {
		t.Fatal("expected error for unsupported SNS Type, got nil")
	}
}
