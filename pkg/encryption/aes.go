// Package encryption provides AES-GCM encryption for sensitive financial data fields.
// Compliant with PCI-DSS requirements for data-at-rest protection.
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext format")
	ErrDecryptionFailed  = errors.New("decryption failed: data may be corrupted or key mismatch")
	ErrKeyTooShort       = errors.New("encryption key must be at least 16 characters")
)

// FieldEncryptor handles AES-256-GCM encryption/decryption of sensitive fields.
// Thread-safe for concurrent use.
type FieldEncryptor struct {
	gcm cipher.AEAD
	key []byte
}

// NewFieldEncryptor creates an encryptor from a passphrase.
// The passphrase is stretched to 32 bytes via SHA-256 for AES-256.
func NewFieldEncryptor(passphrase string) (*FieldEncryptor, error) {
	if len(passphrase) < 16 {
		return nil, ErrKeyTooShort
	}

	// Derive 256-bit key from passphrase
	hash := sha256.Sum256([]byte(passphrase))
	key := hash[:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	return &FieldEncryptor{gcm: gcm, key: key}, nil
}

// Encrypt encrypts plaintext and returns a base64-encoded ciphertext with "enc:" prefix.
// Format: "enc:<base64(nonce + ciphertext)>"
func (e *FieldEncryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	return "enc:" + encoded, nil
}

// Decrypt decrypts a value produced by Encrypt.
// Returns the original plaintext.
func (e *FieldEncryptor) Decrypt(encrypted string) (string, error) {
	if !strings.HasPrefix(encrypted, "enc:") {
		return encrypted, nil // Not encrypted, return as-is
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encrypted, "enc:"))
	if err != nil {
		return "", ErrInvalidCiphertext
	}

	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", ErrInvalidCiphertext
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrDecryptionFailed
	}

	return string(plaintext), nil
}

// IsEncrypted checks if a string value is already encrypted.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, "enc:")
}

// HashSensitive creates a one-way SHA-256 hash for sensitive data (e.g., SSN, tax ID).
// Use this when you need to search/match but not retrieve the original value.
func HashSensitive(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

// MaskField masks a string for display, showing only the last N characters.
// Example: MaskField("4111111111111111", 4) -> "************1111"
func MaskField(value string, showLast int) string {
	if len(value) <= showLast {
		return value
	}
	masked := strings.Repeat("*", len(value)-showLast) + value[len(value)-showLast:]
	return masked
}

// MaskEmail masks an email address for display.
// Example: "john.doe@example.com" -> "j*****e@example.com"
func MaskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || len(parts[0]) < 2 {
		return email
	}
	local := parts[0]
	masked := string(local[0]) + strings.Repeat("*", len(local)-2) + string(local[len(local)-1])
	return masked + "@" + parts[1]
}

// EncryptMap encrypts specified fields in a map.
// Useful for encrypting multiple PII fields before database storage.
func (e *FieldEncryptor) EncryptMap(data map[string]string, fields []string) (map[string]string, error) {
	result := make(map[string]string, len(data))
	for k, v := range data {
		result[k] = v
	}

	for _, field := range fields {
		if val, ok := result[field]; ok && val != "" && !IsEncrypted(val) {
			encrypted, err := e.Encrypt(val)
			if err != nil {
				return nil, fmt.Errorf("encrypt field %s: %w", field, err)
			}
			result[field] = encrypted
		}
	}

	return result, nil
}

// DecryptMap decrypts specified fields in a map.
func (e *FieldEncryptor) DecryptMap(data map[string]string, fields []string) (map[string]string, error) {
	result := make(map[string]string, len(data))
	for k, v := range data {
		result[k] = v
	}

	for _, field := range fields {
		if val, ok := result[field]; ok && IsEncrypted(val) {
			decrypted, err := e.Decrypt(val)
			if err != nil {
				return nil, fmt.Errorf("decrypt field %s: %w", field, err)
			}
			result[field] = decrypted
		}
	}

	return result, nil
}
