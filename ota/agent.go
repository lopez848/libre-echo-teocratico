package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
)

// Service is a program the agent may replace: services/<Name>/<Name>.
type Service struct {
	Name      string
	HealthURL string // empty: healthy when the process stays up
}

// State is what the agent remembers between checks.
type State struct {
	Installed string   `json:"installed"`
	Failed    []string `json:"failed,omitempty"`
}

// Agent checks GitHub Releases and installs signed updates.
type Agent struct {
	Repo      string // "owner/name"
	API       string // https://api.github.com
	HTTP      *http.Client
	Pub       ed25519.PublicKey
	Services  map[string]Service
	Dir       string // services directory
	StateFile string
	Restart   func(name string) error
	Healthy   func(ctx context.Context, s Service) bool
}

// Check installs the latest release when it is signed, newer than the
// installed one and not known to fail. It returns what it did.
func (a *Agent) Check(ctx context.Context) (string, error) {
	assets, err := a.latestAssets(ctx)
	if errors.Is(err, errNoRelease) {
		return "no release published", nil
	}
	if err != nil {
		return "", err
	}
	if assets["manifest.json"] == "" || assets["manifest.json.sig"] == "" {
		return "", errors.New("release has no manifest.json and manifest.json.sig")
	}
	raw, err := a.get(ctx, assets["manifest.json"], 64<<10)
	if err != nil {
		return "", err
	}
	sig, err := a.get(ctx, assets["manifest.json.sig"], 1<<10)
	if err != nil {
		return "", err
	}
	m, err := VerifyManifest(raw, sig, a.Pub, a.Services)
	if err != nil {
		return "", err
	}
	st := a.loadState()
	if !newer(m.Version, st.Installed) {
		return fmt.Sprintf("up to date (%s)", st.Installed), nil
	}
	if slices.Contains(st.Failed, m.Version) {
		return fmt.Sprintf("skipping %s: it failed before", m.Version), nil
	}
	if err := a.install(ctx, m); err != nil {
		st.Failed = append(st.Failed, m.Version)
		a.saveState(st)
		return "", err
	}
	st.Installed = m.Version
	if err := a.saveState(st); err != nil {
		return "", fmt.Errorf("installed %s but could not save state: %w", m.Version, err)
	}
	return "installed " + m.Version, nil
}

var (
	errNoRelease = errors.New("no release")
	errTooLarge  = errors.New("larger than allowed")
)

func (a *Agent) latestAssets(ctx context.Context) (map[string]string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", a.API+"/repos/"+a.Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "libre-echo-ota")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("releases api: %s", resp.Status)
	}
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("releases api: %w", err)
	}
	out := map[string]string{}
	for _, as := range rel.Assets {
		out[as.Name] = as.URL
	}
	return out, nil
}

func (a *Agent) get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "libre-echo-ota")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("get %s: %w (%d bytes)", url, errTooLarge, max)
	}
	return b, nil
}

// install downloads and checks every file first, then swaps them in, keeps
// backups, restarts and checks health; on failure it restores the backups.
func (a *Agent) install(ctx context.Context, m *Manifest) error {
	for _, f := range m.Files {
		if err := a.download(ctx, f); err != nil {
			a.cleanNew(m)
			return err
		}
	}
	var swapped []File
	for _, f := range m.Files {
		bin := a.path(f.Service)
		if err := copyFile(bin, bin+".ota-bak"); err != nil && !os.IsNotExist(err) {
			a.cleanNew(m)
			a.rollback(swapped)
			return fmt.Errorf("%s: backup: %w", f.Service, err)
		}
		if err := os.Rename(bin+".new", bin); err != nil {
			a.cleanNew(m)
			a.rollback(swapped)
			return fmt.Errorf("%s: swap: %w", f.Service, err)
		}
		swapped = append(swapped, f)
	}
	for _, f := range swapped {
		if err := a.Restart(f.Service); err != nil || !a.Healthy(ctx, a.Services[f.Service]) {
			a.rollback(swapped)
			return fmt.Errorf("%s %s was unhealthy: rolled back", f.Service, m.Version)
		}
	}
	return nil
}

func (a *Agent) download(ctx context.Context, f File) error {
	b, err := a.get(ctx, f.URL, f.Size)
	if errors.Is(err, errTooLarge) {
		return fmt.Errorf("%s: sha256 does not match the signed manifest (file too large)", f.Service)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", f.Service, err)
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
		return fmt.Errorf("%s: sha256 does not match the signed manifest", f.Service)
	}
	tmp := a.path(f.Service) + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return fmt.Errorf("%s: %w", f.Service, err)
	}
	return os.Chmod(tmp, 0o755) // WriteFile keeps the mode of an existing file
}

func (a *Agent) rollback(swapped []File) {
	for _, f := range swapped {
		bin := a.path(f.Service)
		if err := os.Rename(bin+".ota-bak", bin); err != nil {
			logf("rollback %s: %v", f.Service, err)
			continue
		}
		if err := a.Restart(f.Service); err != nil {
			logf("rollback restart %s: %v", f.Service, err)
		}
	}
}

func (a *Agent) cleanNew(m *Manifest) {
	for _, f := range m.Files {
		os.Remove(a.path(f.Service) + ".new")
	}
}

func (a *Agent) path(service string) string {
	name := a.Services[service].Name
	return filepath.Join(a.Dir, name, name)
}

func (a *Agent) loadState() State {
	var st State
	if b, err := os.ReadFile(a.StateFile); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (a *Agent) saveState(st State) error {
	if err := os.MkdirAll(filepath.Dir(a.StateFile), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	tmp := a.StateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.StateFile)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".tmp", dst)
}
