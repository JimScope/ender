package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/pocketbase/pocketbase/tools/security"
)

const encryptedPrefix = "enc:"

// deriveKey returns a 32-byte AES key from the WEBHOOK_ENCRYPTION_KEY env var.
func deriveKey() ([]byte, error) {
	raw := os.Getenv("WEBHOOK_ENCRYPTION_KEY")
	if raw == "" {
		return nil, fmt.Errorf("WEBHOOK_ENCRYPTION_KEY not set: webhook secrets will not be encrypted")
	}
	hash := sha256.Sum256([]byte(raw))
	return hash[:], nil
}

// aesGCMEncrypt encrypts plaintext using AES-GCM with the given key,
// returning prefix + base64url(nonce || ciphertext).
func aesGCMEncrypt(key []byte, plaintext, prefix string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(ciphertext), nil
}

// aesGCMDecrypt decrypts a prefix + base64url(nonce || ciphertext) string.
func aesGCMDecrypt(key []byte, value, prefix string) (string, error) {
	data, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(
		strings.TrimPrefix(value, prefix),
	)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// EncryptSecret encrypts a plaintext secret using AES-GCM.
// Returns a base64-encoded string prefixed with "enc:".
// If the value is already encrypted or empty, it is returned as-is.
func EncryptSecret(plaintext string) (string, error) {
	if plaintext == "" || strings.HasPrefix(plaintext, encryptedPrefix) {
		return plaintext, nil
	}

	key, err := deriveKey()
	if err != nil {
		return "", fmt.Errorf("cannot encrypt webhook secret: %w", err)
	}

	return aesGCMEncrypt(key, plaintext, encryptedPrefix)
}

// DecryptSecret decrypts an AES-GCM encrypted secret.
// If the value is not prefixed with "enc:", it is returned as-is (plaintext fallback).
func DecryptSecret(encrypted string) (string, error) {
	if encrypted == "" || !strings.HasPrefix(encrypted, encryptedPrefix) {
		return encrypted, nil // plaintext — backwards compatible
	}

	key, err := deriveKey()
	if err != nil {
		return "", fmt.Errorf("no encryption key: %w", err)
	}

	return aesGCMDecrypt(key, encrypted, encryptedPrefix)
}

// GenerateSecureKey generates a cryptographically secure random key with a prefix.
func GenerateSecureKey(prefix string, length int) string {
	return prefix + security.RandomString(length)
}

// APIKeyHashPrefix marks API key values stored as SHA-256 digests.
const APIKeyHashPrefix = "sha256:"

// HashAPIKey returns the storage representation of an API key: a SHA-256
// digest, hex-encoded and prefixed so hashed values are distinguishable from
// legacy plaintext keys. Lookups hash the presented key and compare digests,
// so a database dump never exposes usable bearer credentials.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return APIKeyHashPrefix + hex.EncodeToString(sum[:])
}

// FindByAPIKey looks up a record whose key field matches the SHA-256 hash of
// rawKey, falling back to a plaintext comparison for keys created before the
// hashing migration (logging a warning so the gap stays visible). The filter
// must contain a {:key} placeholder; any other placeholders are supplied via
// extra. Centralising this here keeps every API-key consumer — device auth,
// integration auth, and the realtime subscription guard — in lockstep.
func FindByAPIKey(app core.App, collection, filter, rawKey string, extra dbx.Params) (*core.Record, error) {
	params := dbx.Params{}
	for k, v := range extra {
		params[k] = v
	}

	params["key"] = HashAPIKey(rawKey)
	if record, err := app.FindFirstRecordByFilter(collection, filter, params); err == nil {
		return record, nil
	}

	params["key"] = rawKey
	record, err := app.FindFirstRecordByFilter(collection, filter, params)
	if err != nil {
		return nil, err
	}
	app.Logger().Warn("matched legacy plaintext API key — hashing migration may not have run",
		slog.String("collection", collection))
	return record, nil
}

// GenerateKeyPrefix returns the first KeyPrefixDisplay characters of a key followed by "...".
func GenerateKeyPrefix(key string) string {
	if len(key) <= KeyPrefixDisplay {
		return key
	}
	return key[:KeyPrefixDisplay] + "..."
}

// RotateAPIKeyResult holds the output of a successful key rotation.
type RotateAPIKeyResult struct {
	NewKey *core.Record
}

// RotateAPIKey deactivates the old key and creates a new one.
// The new key record has "key" unhidden so the caller can read the raw value once.
func RotateAPIKey(app core.App, userId, keyId, expiresAt string) (*RotateAPIKeyResult, error) {
	oldKey, err := app.FindRecordById("api_keys", keyId)
	if err != nil {
		return nil, fmt.Errorf("API key not found")
	}
	if oldKey.GetString("user") != userId {
		return nil, fmt.Errorf("not your API key")
	}
	if !oldKey.GetBool("is_active") {
		return nil, fmt.Errorf("cannot rotate a revoked key")
	}

	// Deactivate + create atomically so a failure cannot leave the user with
	// the old key revoked and no replacement issued.
	var newKey *core.Record
	err = app.RunInTransaction(func(txApp core.App) error {
		oldKey.Set("is_active", false)
		if err := txApp.Save(oldKey); err != nil {
			return fmt.Errorf("failed to deactivate old key: %w", err)
		}

		// Create new key (the OnRecordCreate hook generates the actual key value)
		col, err := txApp.FindCollectionByNameOrId("api_keys")
		if err != nil {
			return fmt.Errorf("api_keys collection not found: %w", err)
		}

		newKey = core.NewRecord(col)
		newKey.Set("name", oldKey.GetString("name")+" (rotated)")
		newKey.Set("user", userId)
		newKey.Set("is_active", true)
		if expiresAt != "" {
			newKey.Set("expires_at", expiresAt)
		}

		if err := txApp.Save(newKey); err != nil {
			return fmt.Errorf("failed to create new key: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Unhide key so the caller can read the raw value (shown once)
	newKey.Unhide("key")

	return &RotateAPIKeyResult{NewKey: newKey}, nil
}
