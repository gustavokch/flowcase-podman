// Package auth holds password + session + Authentik plumbing for the
// orchestrator. password.go is the bcrypt + random-token half: hashing,
// constant-time comparison, and the alphanumeric auth_token the legacy
// Python writes into the user table.
package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/bcrypt"
)

// AuthTokenLen is the length of the per-user auth_token written to the
// `user` table. Matches auth.py:234-235.
const AuthTokenLen = 80

// alphabet is the [A-Za-z0-9] set the Python `random.choice` walks for
// passwords + tokens. Kept identical so DB strings look the same shape
// across versions.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Hash returns a bcrypt hash suitable for storing in user.password.
// Uses bcrypt.DefaultCost (currently 10) — same default the legacy
// flask_bcrypt.generate_password_hash uses.
func Hash(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password may not be empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(h), nil
}

// Check returns true iff `plain` matches the bcrypt-hashed `hashed`.
// Constant-time inside bcrypt's compare; safe against timing leaks.
// Returns false (not error) on mismatch — callers don't need to
// distinguish "wrong password" from "hash unparsable" since the
// surfaced response is the same 401 either way.
func Check(hashed, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
}

// GenerateAuthToken returns AuthTokenLen alphanumeric characters drawn
// from crypto/rand. Replaces auth.py:234-235.
func GenerateAuthToken() (string, error) {
	return randomAlphanumeric(AuthTokenLen)
}

// GenerateRandomPassword returns n alphanumeric characters drawn from
// crypto/rand. Used for external (Authentik) users where the legacy
// code at auth.py:45 generates 16 chars.
func GenerateRandomPassword(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("GenerateRandomPassword: n must be positive, got %d", n)
	}
	return randomAlphanumeric(n)
}

func randomAlphanumeric(n int) (string, error) {
	out := make([]byte, n)
	limit := big.NewInt(int64(len(alphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[idx.Int64()]
	}
	return string(out), nil
}
