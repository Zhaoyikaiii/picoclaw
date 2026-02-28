// PicoClaw - Ultra-lightweight personal AI agent
// Swarm cryptographic utilities for secure inter-node communication
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Signer creates and verifies HMAC-SHA256 signatures.
type Signer struct {
	secret  []byte
	enabled bool
}

// NewSigner creates a new HMAC signer.
func NewSigner(secret string) *Signer {
	s := &Signer{enabled: secret != ""}
	if s.enabled {
		// Support both base64-encoded and raw secrets
		if decoded, err := base64.StdEncoding.DecodeString(secret); err == nil && len(decoded) > 0 {
			s.secret = decoded
		} else {
			s.secret = []byte(secret)
		}
	}
	return s
}

// Sign computes HMAC-SHA256 of the canonical JSON representation of data.
func (s *Signer) Sign(data any) (string, error) {
	if !s.enabled {
		return "", nil
	}

	// Create canonical JSON (sorted keys)
	canonical, err := canonicalJSON(data)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize: %w", err)
	}

	h := hmac.New(sha256.New, s.secret)
	h.Write(canonical)
	sig := h.Sum(nil)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify verifies the HMAC-SHA256 signature of data.
func (s *Signer) Verify(data any, signature string) bool {
	if !s.enabled {
		return true // Disabled means accept all
	}

	if signature == "" {
		return false
	}

	expected, err := s.Sign(data)
	if err != nil {
		return false
	}

	return hmac.Equal([]byte(signature), []byte(expected))
}

// Encryptor encrypts/decrypts session data using AES-GCM.
type Encryptor struct {
	key     []byte
	enabled bool
}

// NewEncryptor creates a new AES-GCM encryptor.
func NewEncryptor(key string) (*Encryptor, error) {
	if key == "" {
		return &Encryptor{enabled: false}, nil
	}

	// Support both base64-encoded and raw keys
	var keyBytes []byte
	if decoded, err := base64.StdEncoding.DecodeString(key); err == nil && len(decoded) > 0 {
		keyBytes = decoded
	} else {
		keyBytes = []byte(key)
	}

	// AES-256 requires 32-byte key
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes for AES-256, got %d bytes", len(keyBytes))
	}

	return &Encryptor{
		key:     keyBytes,
		enabled: true,
	}, nil
}

// EncryptedData wraps encrypted content with nonce.
type EncryptedData struct {
	Nonce  string `json:"nonce"`
	Cipher string `json:"cipher"`
}

// Encrypt encrypts the provided data using AES-GCM.
func (e *Encryptor) Encrypt(data []byte) (*EncryptedData, error) {
	if !e.enabled {
		return nil, nil
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	cipherText := gcm.Seal(nonce, nonce, data, nil)

	return &EncryptedData{
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Cipher: base64.StdEncoding.EncodeToString(cipherText),
	}, nil
}

// Decrypt decrypts the provided data using AES-GCM.
func (e *Encryptor) Decrypt(enc *EncryptedData) ([]byte, error) {
	if !e.enabled || enc == nil {
		return nil, nil
	}

	nonce, err := base64.StdEncoding.DecodeString(enc.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce encoding: %w", err)
	}

	cipherText, err := base64.StdEncoding.DecodeString(enc.Cipher)
	if err != nil {
		return nil, fmt.Errorf("invalid cipher encoding: %w", err)
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	if len(cipherText) < gcm.NonceSize() {
		return nil, fmt.Errorf("cipher text too short")
	}

	plaintext, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// canonicalJSON creates a canonical JSON representation with sorted keys.
func canonicalJSON(data any) ([]byte, error) {
	// Marshal to JSON then unmarshal into map[string]any for sorting
	j, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var raw map[string]any
	if json.Unmarshal(j, &raw) == nil {
		// Successfully parsed as object, sort keys
		return json.Marshal(sortMap(raw))
	}

	// Not an object, return as-is
	return j, nil
}

func sortMap(m map[string]any) map[string]any {
	sorted := make(map[string]any)
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			sorted[k] = sortMap(val)
		case []any:
			sorted[k] = sortSlice(val)
		default:
			sorted[k] = v
		}
	}
	return sorted
}

func sortSlice(s []any) []any {
	sorted := make([]any, len(s))
	for i, v := range s {
		switch val := v.(type) {
		case map[string]any:
			sorted[i] = sortMap(val)
		case []any:
			sorted[i] = sortSlice(val)
		default:
			sorted[i] = v
		}
	}
	return sorted
}

// GenerateKey generates a random 32-byte key for AES-256 encryption.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// GenerateSecret generates a random HMAC secret.
func GenerateSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(secret), nil
}

// NormalizeKey ensures a key is exactly 32 bytes by hashing or truncating.
func NormalizeKey(key string) string {
	if len(key) >= 32 {
		return key[:32]
	}
	h := sha256.Sum256([]byte(key))
	return string(h[:])
}

// ComputeMessageSignature creates a signature from key components for manual signing.
func ComputeMessageSignature(secret string, parts ...string) string {
	var s strings.Builder
	for i, p := range parts {
		if i > 0 {
			s.WriteByte('|')
		}
		s.WriteString(p)
	}

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(s.String()))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// VerifyMessageSignature verifies a manually created signature.
func VerifyMessageSignature(secret, signature string, parts ...string) bool {
	expected := ComputeMessageSignature(secret, parts...)
	return hmac.Equal([]byte(signature), []byte(expected))
}
