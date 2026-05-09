package droplet

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flowcase/flowcase/internal/models"
)

func sampleVNCDroplet() *models.Droplet {
	return &models.Droplet{
		ID:             "d-vnc",
		DisplayName:    "VNC",
		DropletType:    "vnc",
		ServerIP:       ptr("10.0.0.42"),
		ServerPort:     ptr(5901),
		ServerUsername: ptr("kasm_user"),
		ServerPassword: ptr("secretsecret"),
	}
}

func sampleUser() *models.User {
	return &models.User{
		ID:        "u",
		Username:  "alice",
		AuthToken: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd", // 80 chars
	}
}

func TestEncryptGuacTokenIsRoundTrippable(t *testing.T) {
	d := sampleVNCDroplet()
	u := sampleUser()

	token, err := EncryptGuacToken(d, u)
	if err != nil {
		t.Fatalf("EncryptGuacToken: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}

	plaintext, err := decryptGuacToken(token, []byte(u.AuthToken)[:GuacKeyLen])
	if err != nil {
		t.Fatalf("decryptGuacToken: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(plaintext, &got); err != nil {
		t.Fatalf("plaintext is not JSON: %q (err %v)", plaintext, err)
	}
	conn, _ := got["connection"].(map[string]any)
	if conn == nil {
		t.Fatalf("missing connection field: %v", got)
	}
	if conn["type"] != "vnc" {
		t.Errorf("type = %v, want vnc", conn["type"])
	}
	settings, _ := conn["settings"].(map[string]any)
	if settings == nil {
		t.Fatal("missing settings")
	}
	if settings["hostname"] != "10.0.0.42" {
		t.Errorf("hostname = %v", settings["hostname"])
	}
	// JSON numbers come back as float64; compare via numeric value.
	if port, _ := settings["port"].(float64); int(port) != 5901 {
		t.Errorf("port = %v", settings["port"])
	}
	if settings["username"] != "kasm_user" {
		t.Errorf("username = %v", settings["username"])
	}
	if settings["password"] != "secretsecret" {
		t.Errorf("password = %v", settings["password"])
	}
	if settings["disable-copy"] != "false" || settings["disable-paste"] != "false" {
		t.Errorf("disable-* not stamped to false: copy=%v paste=%v",
			settings["disable-copy"], settings["disable-paste"])
	}
}

func TestEncryptGuacTokenRejectsShortAuthToken(t *testing.T) {
	d := sampleVNCDroplet()
	u := &models.User{ID: "u", AuthToken: "short"}

	_, err := EncryptGuacToken(d, u)
	if err == nil {
		t.Error("expected error for AuthToken shorter than 32 bytes")
	}
}

func TestEncryptGuacTokenIVDiffersAcrossCalls(t *testing.T) {
	d := sampleVNCDroplet()
	u := sampleUser()

	a, _ := EncryptGuacToken(d, u)
	b, _ := EncryptGuacToken(d, u)
	if a == b {
		t.Error("two encryptions of the same payload produced identical tokens — IV not random")
	}
}

func TestEncryptGuacTokenHandlesNilFields(t *testing.T) {
	d := &models.Droplet{
		ID:          "d",
		DisplayName: "X",
		DropletType: "rdp",
		// All server_* nil — Python emits None / JSON null.
	}
	u := sampleUser()
	token, err := EncryptGuacToken(d, u)
	if err != nil {
		t.Fatalf("EncryptGuacToken: %v", err)
	}
	plaintext, err := decryptGuacToken(token, []byte(u.AuthToken)[:GuacKeyLen])
	if err != nil {
		t.Fatalf("decryptGuacToken: %v", err)
	}

	// All nullable fields should round-trip as JSON null, not as
	// the zero value of their dereffed type.
	var got map[string]any
	_ = json.Unmarshal(plaintext, &got)
	settings := got["connection"].(map[string]any)["settings"].(map[string]any)
	for _, k := range []string{"hostname", "username", "password", "port"} {
		if settings[k] != nil {
			t.Errorf("settings[%q] = %v, want JSON null for nil pointer", k, settings[k])
		}
	}
}

func TestPKCS7PadUnpadRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"a",
		"sixteenbytechars",   // exactly one block — must add a full extra block
		"thirtyonebytestring--padded__abc",
	}
	for _, in := range cases {
		padded := pkcs7Pad([]byte(in), 16)
		if len(padded)%16 != 0 {
			t.Errorf("pkcs7Pad(%q) -> %d bytes, not aligned", in, len(padded))
		}
		out, err := pkcs7Unpad(padded, 16)
		if err != nil {
			t.Fatalf("pkcs7Unpad: %v", err)
		}
		if !bytes.Equal([]byte(in), out) {
			t.Errorf("round-trip mismatch: %q -> %q", in, out)
		}
	}
}

// TestEncryptGuacTokenDecryptsWithPython is the cross-language
// compatibility test the plan calls out. It runs when the test
// machine has Python with pycryptodome installed; otherwise it
// skips. The script lives at testdata/decrypt_token.py.
func TestEncryptGuacTokenDecryptsWithPython(t *testing.T) {
	pyPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not on PATH: %v", err)
	}
	// Confirm pycryptodome is importable; the legacy code uses it.
	probe := exec.Command(pyPath, "-c", "import Crypto.Cipher.AES")
	if err := probe.Run(); err != nil {
		t.Skipf("pycryptodome not available: %v", err)
	}

	d := sampleVNCDroplet()
	u := sampleUser()
	token, err := EncryptGuacToken(d, u)
	if err != nil {
		t.Fatalf("EncryptGuacToken: %v", err)
	}

	script := filepath.Join("testdata", "decrypt_token.py")
	cmd := exec.Command(pyPath, script, u.AuthToken, token)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python decrypt failed: %v (stderr: %s)", err, errOutput(err))
	}
	if !strings.Contains(string(out), `"hostname": "10.0.0.42"`) ||
		!strings.Contains(string(out), `"port": 5901`) {
		t.Errorf("python-decrypted JSON missing expected fields: %s", out)
	}
}

func errOutput(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(exitErr.Stderr)
	}
	return ""
}
