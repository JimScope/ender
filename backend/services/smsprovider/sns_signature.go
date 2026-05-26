// Package smsprovider — SNS signature verification.
//
// AWS End User Messaging emits delivery events through SNS topics. SNS HTTPS
// subscriptions are public endpoints, so every payload is signed with an
// RSA-SHA1 (SignatureVersion=1) or RSA-SHA256 (SignatureVersion=2) signature
// produced with a per-region cert that AWS rotates. This file implements the
// minimal verification path documented at
// https://docs.aws.amazon.com/sns/latest/dg/sns-verify-signature-of-message.html
// so we can trust the inbound webhook before mutating sms_messages.
package smsprovider

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SNSMessage is the wire format of any inbound SNS notification. Only the
// fields we actually verify or branch on are decoded; AWS may add new fields
// without breaking us.
type SNSMessage struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn"`
	Subject          string `json:"Subject,omitempty"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	Token            string `json:"Token,omitempty"`
	SubscribeURL     string `json:"SubscribeURL,omitempty"`
	UnsubscribeURL   string `json:"UnsubscribeURL,omitempty"`
}

var (
	certCache     = make(map[string]cachedCert)
	certCacheLock sync.RWMutex
	certCacheTTL  = time.Hour
	// SNS signing cert URLs come from sns.<region>.amazonaws.com or
	// sns.<region>.amazonaws.com.cn (China regions). Match either.
	// Kept as a package-private var so tests can swap it temporarily when
	// pointing at a local httptest server.
	awsSNSHostRe = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?$`)
	httpClient   = &http.Client{Timeout: 10 * time.Second}
)

type cachedCert struct {
	pub    *rsa.PublicKey
	expiry time.Time
}

// VerifySNSSignature verifies the RSA signature on the supplied SNS message.
// Returns nil on success, an error describing the failure otherwise. Callers
// should treat any error as authentication failure and return HTTP 401.
func VerifySNSSignature(msg *SNSMessage) error {
	if msg == nil {
		return errors.New("nil SNS message")
	}
	if msg.Signature == "" || msg.SigningCertURL == "" {
		return errors.New("missing Signature or SigningCertURL")
	}
	if err := validateCertURL(msg.SigningCertURL); err != nil {
		return err
	}

	pub, err := fetchCertPublicKey(msg.SigningCertURL)
	if err != nil {
		return err
	}

	canonical, err := buildCanonicalString(msg)
	if err != nil {
		return err
	}
	sigBytes, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}

	switch msg.SignatureVersion {
	case "1":
		sum := sha1.Sum([]byte(canonical))
		return rsa.VerifyPKCS1v15(pub, crypto.SHA1, sum[:], sigBytes)
	case "2", "":
		// AWS defaults newer topics to SignatureVersion 2 (SHA256). Empty
		// SignatureVersion is treated as v2 to match the verification path
		// some AWS regions emit.
		sum := sha256.Sum256([]byte(canonical))
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sigBytes)
	default:
		return fmt.Errorf("unsupported SignatureVersion %q", msg.SignatureVersion)
	}
}

// validateCertURL enforces three guards before we even fetch the cert:
// HTTPS scheme, .pem path suffix, and AWS-owned host. Any failure short-circuits
// the verification so a malicious payload cannot trick us into fetching an
// attacker-controlled certificate.
func validateCertURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid SigningCertURL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("SigningCertURL must be HTTPS")
	}
	if !strings.HasSuffix(u.Path, ".pem") {
		return errors.New("SigningCertURL must end in .pem")
	}
	if !awsSNSHostRe.MatchString(u.Host) {
		return fmt.Errorf("SigningCertURL host %q is not an AWS SNS endpoint", u.Host)
	}
	return nil
}

// fetchCertPublicKey returns the RSA public key embedded in the SNS signing
// certificate at certURL. Successful lookups are cached for certCacheTTL so a
// burst of SNS notifications does not turn into N HTTPS round-trips.
func fetchCertPublicKey(certURL string) (*rsa.PublicKey, error) {
	certCacheLock.RLock()
	cached, ok := certCache[certURL]
	certCacheLock.RUnlock()
	if ok && time.Now().Before(cached.expiry) {
		return cached.pub, nil
	}

	resp, err := httpClient.Get(certURL)
	if err != nil {
		return nil, fmt.Errorf("download cert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download cert: HTTP %d", resp.StatusCode)
	}
	pemBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read cert body: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("cert is not PEM-encoded")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cert: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("cert public key is not RSA")
	}

	certCacheLock.Lock()
	certCache[certURL] = cachedCert{pub: pub, expiry: time.Now().Add(certCacheTTL)}
	certCacheLock.Unlock()
	return pub, nil
}

// buildCanonicalString produces the SNS canonical string in the order AWS
// requires for signature computation. The trailing newline at the end of each
// field is part of the spec — do not collapse them.
func buildCanonicalString(msg *SNSMessage) (string, error) {
	var b strings.Builder
	switch msg.Type {
	case "Notification":
		b.WriteString("Message\n")
		b.WriteString(msg.Message)
		b.WriteByte('\n')
		b.WriteString("MessageId\n")
		b.WriteString(msg.MessageID)
		b.WriteByte('\n')
		if msg.Subject != "" {
			b.WriteString("Subject\n")
			b.WriteString(msg.Subject)
			b.WriteByte('\n')
		}
		b.WriteString("Timestamp\n")
		b.WriteString(msg.Timestamp)
		b.WriteByte('\n')
		b.WriteString("TopicArn\n")
		b.WriteString(msg.TopicARN)
		b.WriteByte('\n')
		b.WriteString("Type\n")
		b.WriteString(msg.Type)
		b.WriteByte('\n')
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		b.WriteString("Message\n")
		b.WriteString(msg.Message)
		b.WriteByte('\n')
		b.WriteString("MessageId\n")
		b.WriteString(msg.MessageID)
		b.WriteByte('\n')
		b.WriteString("SubscribeURL\n")
		b.WriteString(msg.SubscribeURL)
		b.WriteByte('\n')
		b.WriteString("Timestamp\n")
		b.WriteString(msg.Timestamp)
		b.WriteByte('\n')
		b.WriteString("Token\n")
		b.WriteString(msg.Token)
		b.WriteByte('\n')
		b.WriteString("TopicArn\n")
		b.WriteString(msg.TopicARN)
		b.WriteByte('\n')
		b.WriteString("Type\n")
		b.WriteString(msg.Type)
		b.WriteByte('\n')
	default:
		return "", fmt.Errorf("unsupported SNS Type %q", msg.Type)
	}
	return b.String(), nil
}

// ConfirmSNSSubscription performs the GET against SubscribeURL that finalises
// the SNS subscription. Errors are returned to the caller so they can log
// them; SNS will retry the SubscriptionConfirmation on the next delivery
// attempt if we fail to confirm.
func ConfirmSNSSubscription(subscribeURL string) error {
	resp, err := httpClient.Get(subscribeURL)
	if err != nil {
		return fmt.Errorf("subscribe GET failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("subscribe GET returned HTTP %d", resp.StatusCode)
	}
	return nil
}
