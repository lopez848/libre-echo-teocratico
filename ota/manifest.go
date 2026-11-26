// Package main is the OTA agent that runs on each LibreEcho speaker. It
// pulls signed releases from GitHub Releases and installs them with backup,
// health check and automatic rollback (docs/actualizaciones-remotas.md).
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Manifest lists the files of one release. Its exact bytes are signed with
// the owner's Ed25519 key; the signature travels as a separate asset.
type Manifest struct {
	Version string `json:"version"`
	Files   []File `json:"files"`
}

// File is one binary of a release, installed as services/<Service>/<Service>.
type File struct {
	Service string `json:"service"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

const maxFileSize = 32 << 20

var reSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VerifyManifest checks the signature over the raw manifest bytes and then
// validates every field. Nothing is trusted before the signature passes.
func VerifyManifest(raw, sig []byte, pub ed25519.PublicKey, services map[string]Service) (*Manifest, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("no valid public key built in")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return nil, errors.New("malformed signature")
	}
	if !ed25519.Verify(pub, raw, s) {
		return nil, errors.New("bad signature")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if _, err := parseVersion(m.Version); err != nil {
		return nil, err
	}
	if len(m.Files) == 0 {
		return nil, errors.New("manifest has no files")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if _, ok := services[f.Service]; !ok {
			return nil, fmt.Errorf("service %q is not updatable", f.Service)
		}
		if seen[f.Service] {
			return nil, fmt.Errorf("service %q listed twice", f.Service)
		}
		seen[f.Service] = true
		u, err := url.Parse(f.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fmt.Errorf("%s: url must be https", f.Service)
		}
		if !reSHA.MatchString(f.SHA256) {
			return nil, fmt.Errorf("%s: bad sha256", f.Service)
		}
		if f.Size <= 0 || f.Size > maxFileSize {
			return nil, fmt.Errorf("%s: bad size %d", f.Service, f.Size)
		}
	}
	return &m, nil
}

// parseVersion accepts "1.2.3" or "v1.2.3".
func parseVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("bad version %q", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return out, fmt.Errorf("bad version %q", v)
		}
		out[i] = n
	}
	return out, nil
}

// newer reports whether version a is greater than b. An unparsable b counts
// as 0.0.0; an unparsable a is never newer.
func newer(a, b string) bool {
	va, err := parseVersion(a)
	if err != nil {
		return false
	}
	vb, _ := parseVersion(b)
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}
