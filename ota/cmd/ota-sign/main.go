// Command ota-sign runs on the owner's PC: it creates the signing key and
// writes and signs the manifest of a release. The private key never leaves
// the PC and must never be committed.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usage = `usage:
  ota-sign keygen <key-file>                 create the key (mode 600), print the public key
  ota-sign pubkey <key-file>                 print the public key
  ota-sign manifest <version> <url-prefix> <service>=<file>...   write manifest.json
  ota-sign sign <key-file> <manifest.json>   write <manifest.json>.sig`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ota-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch {
	case args[0] == "keygen" && len(args) == 2:
		pub, err := keygen(args[1])
		if err != nil {
			return err
		}
		fmt.Println(pub)
	case args[0] == "pubkey" && len(args) == 2:
		key, err := loadKey(args[1])
		if err != nil {
			return err
		}
		fmt.Println(base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	case args[0] == "manifest" && len(args) >= 4:
		b, err := manifest(args[1], args[2], args[3:])
		if err != nil {
			return err
		}
		return os.WriteFile("manifest.json", b, 0o644)
	case args[0] == "sign" && len(args) == 3:
		return sign(args[1], args[2])
	default:
		return errors.New(usage)
	}
	return nil
}

func keygen(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; refusing to overwrite the signing key", path)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key.Seed())+"\n"), 0o600); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("malformed key file")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

type file struct {
	Service string `json:"service"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// manifest builds the manifest for files given as service=path; each asset
// is published under url-prefix with its base name.
func manifest(version, prefix string, specs []string) ([]byte, error) {
	m := struct {
		Version string `json:"version"`
		Files   []file `json:"files"`
	}{Version: version}
	for _, s := range specs {
		svc, path, ok := strings.Cut(s, "=")
		if !ok || svc == "" || path == "" {
			return nil, fmt.Errorf("bad file spec %q, want service=path", s)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		m.Files = append(m.Files, file{Service: svc, URL: strings.TrimRight(prefix, "/") + "/" + filepath.Base(path),
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))})
	}
	return json.MarshalIndent(m, "", "  ")
}

func sign(keyPath, manifestPath string) error {
	key, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw))
	return os.WriteFile(manifestPath+".sig", []byte(sig+"\n"), 0o644)
}
