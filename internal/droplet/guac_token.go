package droplet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flowcase/flowcase/internal/models"
)

// GuacKeyLen is the byte length of the AES-256-CBC key used to encrypt
// guac tokens. The legacy Python slices `auth_token.encode()[:32]` at
// droplet.py:597; ours does the equivalent via len() on the auth-token
// byte slice. Auth tokens are 80 alphanumeric characters (T2.7), so
// the slice never falls short.
const GuacKeyLen = 32

// EncryptGuacToken builds the wire-compatible AES-256-CBC envelope the
// flowcase-guac bridge consumes. Mirrors generate_guac_token at
// routes/droplet.py:578-616:
//
//  1. Build inner JSON {"connection": {"type": ..., "settings": {...}}}.
//  2. Generate a 16-byte random IV.
//  3. Use user.AuthToken[:32] as the AES-256 key.
//  4. AES-256-CBC encrypt with PKCS#7 padding.
//  5. Wrap in {"iv": b64(IV), "value": b64(ciphertext)} JSON.
//  6. base64 the whole thing.
//
// The `disable-copy` / `disable-paste` settings are stamped to "false"
// to match the legacy comment ("keep clipboard bi-directional").
func EncryptGuacToken(d *models.Droplet, u *models.User) (string, error) {
	if d == nil || u == nil {
		return "", errors.New("EncryptGuacToken: droplet and user required")
	}
	if len(u.AuthToken) < GuacKeyLen {
		return "", fmt.Errorf("auth token must be at least %d bytes, got %d", GuacKeyLen, len(u.AuthToken))
	}
	key := []byte(u.AuthToken)[:GuacKeyLen]

	plaintext, err := buildInnerJSON(d)
	if err != nil {
		return "", fmt.Errorf("building inner JSON: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("generating IV: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}
	enc := cipher.NewCBCEncrypter(block, iv)
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	enc.CryptBlocks(ciphertext, padded)

	envelope, err := json.Marshal(struct {
		IV    string `json:"iv"`
		Value string `json:"value"`
	}{
		IV:    base64.StdEncoding.EncodeToString(iv),
		Value: base64.StdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(envelope), nil
}

// buildInnerJSON marshals the connection payload. Field order doesn't
// matter for wire compatibility — the receiver parses JSON, not bytes
// — but we keep the same shape Python produces so cross-language
// fixtures look familiar.
//
// nil pointer fields are emitted as JSON null, matching Python where
// `droplet.server_ip` would be None.
func buildInnerJSON(d *models.Droplet) ([]byte, error) {
	settings := map[string]any{
		"hostname":      derefStr(d.ServerIP),
		"username":      derefStr(d.ServerUsername),
		"password":      derefStr(d.ServerPassword),
		"port":          derefInt(d.ServerPort),
		"disable-copy":  "false",
		"disable-paste": "false",
	}
	body := map[string]any{
		"connection": map[string]any{
			"type":     d.DropletType,
			"settings": settings,
		},
	}
	return json.Marshal(body)
}

// derefStr returns *p as a string, or nil (-> JSON null) if p is nil.
func derefStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// derefInt returns *p as an int, or nil (-> JSON null) if p is nil.
func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// pkcs7Pad appends padding bytes so len(out) % blockSize == 0. The
// padding byte value equals the number of padding bytes appended, per
// PKCS#7. A full block of padding is appended when input is already
// aligned, so unpadding never gets confused.
func pkcs7Pad(src []byte, blockSize int) []byte {
	padLen := blockSize - len(src)%blockSize
	out := make([]byte, len(src)+padLen)
	copy(out, src)
	for i := len(src); i < len(out); i++ {
		out[i] = byte(padLen)
	}
	return out
}

// pkcs7Unpad strips PKCS#7 padding and validates it. Used by tests to
// round-trip our own ciphertext; not exposed publicly because the
// orchestrator never decrypts these tokens.
func pkcs7Unpad(src []byte, blockSize int) ([]byte, error) {
	if len(src) == 0 || len(src)%blockSize != 0 {
		return nil, errors.New("pkcs7Unpad: invalid input length")
	}
	padLen := int(src[len(src)-1])
	if padLen == 0 || padLen > blockSize {
		return nil, errors.New("pkcs7Unpad: bad pad length")
	}
	for i := len(src) - padLen; i < len(src); i++ {
		if src[i] != byte(padLen) {
			return nil, errors.New("pkcs7Unpad: corrupt padding")
		}
	}
	return src[:len(src)-padLen], nil
}

// decryptGuacToken is the test-side inverse of EncryptGuacToken. Used
// only by guac_token_test.go to round-trip our own output.
func decryptGuacToken(token string, key []byte) ([]byte, error) {
	if len(key) != GuacKeyLen {
		return nil, fmt.Errorf("key must be %d bytes, got %d", GuacKeyLen, len(key))
	}
	envelopeBytes, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("outer base64: %w", err)
	}
	var env struct {
		IV    string `json:"iv"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(envelopeBytes, &env); err != nil {
		return nil, fmt.Errorf("envelope JSON: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil {
		return nil, fmt.Errorf("iv base64: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("iv must be %d bytes, got %d", aes.BlockSize, len(iv))
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Value)
	if err != nil {
		return nil, fmt.Errorf("ciphertext base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext not block-aligned")
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext, aes.BlockSize)
}
