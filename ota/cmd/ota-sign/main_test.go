package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeygenManifestAndSignRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	keyPath := filepath.Join(dir, "keys", "ota.key")
	pubB64, err := keygen(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(keyPath); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v, want 600", st.Mode().Perm())
	}
	if _, err := keygen(keyPath); err == nil {
		t.Fatal("keygen must never overwrite an existing key")
	}
	os.WriteFile("puente-armv7", []byte("binary"), 0o755)
	if err := run([]string{"manifest", "v1.2.3", "https://github.com/o/r/releases/download/v1.2.3/", "puente=puente-armv7"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", keyPath, "manifest.json"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("manifest.json")
	sigB64, _ := os.ReadFile("manifest.json.sig")
	pub, _ := base64.StdEncoding.DecodeString(pubB64)
	sig, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if !ed25519.Verify(pub, raw, sig) {
		t.Fatal("signature does not verify with the printed public key")
	}
	var m struct {
		Version string
		Files   []file
	}
	json.Unmarshal(raw, &m)
	if m.Version != "v1.2.3" || len(m.Files) != 1 || m.Files[0].URL != "https://github.com/o/r/releases/download/v1.2.3/puente-armv7" ||
		m.Files[0].Size != 6 || m.Files[0].SHA256 != "9a3a45d01531a20e89ac6ae10b0b0beb0492acd7216a368aa062d1a5fecaf9cd" {
		t.Fatalf("manifest %+v", m)
	}
}
