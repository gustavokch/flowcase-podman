package auth

import (
	"strings"
	"testing"
)

func TestHashCheckRoundTrip(t *testing.T) {
	const plain = "correct horse battery staple"
	hashed, err := Hash(plain)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if hashed == plain {
		t.Fatal("Hash should not return plaintext")
	}
	if !Check(hashed, plain) {
		t.Error("Check failed for correct password")
	}
}

func TestCheckRejectsWrongPassword(t *testing.T) {
	hashed, _ := Hash("right")
	if Check(hashed, "wrong") {
		t.Error("Check should fail for wrong password")
	}
	if Check(hashed, "") {
		t.Error("Check should fail for empty password")
	}
}

func TestCheckRejectsGarbageHash(t *testing.T) {
	if Check("not-a-bcrypt-hash", "anything") {
		t.Error("Check should fail on malformed hash")
	}
}

func TestHashRejectsEmptyPassword(t *testing.T) {
	if _, err := Hash(""); err == nil {
		t.Error("Hash(\"\") should error")
	}
}

func TestHashesAreSalted(t *testing.T) {
	// Two hashes of the same password should differ — bcrypt salts.
	a, _ := Hash("same")
	b, _ := Hash("same")
	if a == b {
		t.Error("bcrypt hashes should be salted (different each call)")
	}
}

func TestGenerateAuthTokenLengthAndAlphabet(t *testing.T) {
	tok, err := GenerateAuthToken()
	if err != nil {
		t.Fatalf("GenerateAuthToken: %v", err)
	}
	if len(tok) != AuthTokenLen {
		t.Errorf("len = %d, want %d", len(tok), AuthTokenLen)
	}
	for _, c := range tok {
		if !strings.ContainsRune(alphabet, c) {
			t.Fatalf("token contains non-alphanumeric byte: %q", c)
		}
	}
}

func TestGenerateAuthTokenDistinct(t *testing.T) {
	a, _ := GenerateAuthToken()
	b, _ := GenerateAuthToken()
	if a == b {
		t.Error("two generated tokens collided (probability ~ 62^-80 — assume RNG is broken)")
	}
}

func TestGenerateRandomPasswordHonorsLength(t *testing.T) {
	for _, n := range []int{1, 8, 16, 32} {
		p, err := GenerateRandomPassword(n)
		if err != nil {
			t.Fatalf("GenerateRandomPassword(%d): %v", n, err)
		}
		if len(p) != n {
			t.Errorf("len(%d) = %d, want %d", n, len(p), n)
		}
	}
}

func TestGenerateRandomPasswordRejectsZero(t *testing.T) {
	if _, err := GenerateRandomPassword(0); err == nil {
		t.Error("GenerateRandomPassword(0) should error")
	}
	if _, err := GenerateRandomPassword(-3); err == nil {
		t.Error("GenerateRandomPassword(-3) should error")
	}
}
