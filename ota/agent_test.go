package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub serves the latest-release API and its assets over TLS.
type fakeGitHub struct {
	srv      *httptest.Server
	mu       sync.Mutex
	manifest []byte
	sig      []byte
	files    map[string][]byte // asset name -> bytes
	noRel    bool
	fetches  int // binary downloads
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{files: map[string][]byte{}}
	g.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.URL.Path == "/repos/o/r/releases/latest":
			if g.noRel {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "x", "assets": []map[string]string{
				{"name": "manifest.json", "browser_download_url": g.srv.URL + "/dl/manifest.json"},
				{"name": "manifest.json.sig", "browser_download_url": g.srv.URL + "/dl/manifest.json.sig"},
			}})
		case r.URL.Path == "/dl/manifest.json":
			w.Write(g.manifest)
		case r.URL.Path == "/dl/manifest.json.sig":
			w.Write(g.sig)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			b, ok := g.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			g.fetches++
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// publish signs a release with key; content is what the asset really serves,
// declared is what the manifest claims (normally the same).
func (g *fakeGitHub) publish(key ed25519.PrivateKey, version string, declared, content []byte) {
	sum := sha256.Sum256(declared)
	m, _ := json.Marshal(Manifest{Version: version, Files: []File{{
		Service: "puente", URL: g.srv.URL + "/dl/puente-" + version,
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(declared)),
	}}})
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manifest = m
	g.sig = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, m)))
	g.files["puente-"+version] = content
}

type rig struct {
	a        *Agent
	g        *fakeGitHub
	key      ed25519.PrivateKey
	bin      string
	restarts []string
	healthy  bool
}

func newRig(t *testing.T) *rig {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "services", "puente"), 0o755)
	bin := filepath.Join(dir, "services", "puente", "puente")
	os.WriteFile(bin, []byte("old binary"), 0o755)
	r := &rig{g: newFakeGitHub(t), key: key, bin: bin, healthy: true}
	r.a = &Agent{
		Repo: "o/r", API: r.g.srv.URL, HTTP: r.g.srv.Client(), Pub: pub,
		Services:  map[string]Service{"puente": {Name: "puente"}},
		Dir:       filepath.Join(dir, "services"),
		StateFile: filepath.Join(dir, "ota", "state.json"),
		Restart:   func(name string) error { r.restarts = append(r.restarts, name); return nil },
		Healthy:   func(context.Context, Service) bool { return r.healthy },
	}
	return r
}

func (r *rig) binary(t *testing.T) string {
	b, err := os.ReadFile(r.bin)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallsANewSignedRelease(t *testing.T) {
	r := newRig(t)
	r.g.publish(r.key, "v1.0.0", []byte("new binary"), []byte("new binary"))
	got, err := r.a.Check(context.Background())
	if err != nil || got != "installed v1.0.0" {
		t.Fatalf("Check = %q, %v", got, err)
	}
	if r.binary(t) != "new binary" {
		t.Fatal("new binary not in place")
	}
	if st, _ := os.Stat(r.bin); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
	if b, _ := os.ReadFile(r.bin + ".ota-bak"); string(b) != "old binary" {
		t.Fatal("the previous binary must be kept as a backup")
	}
	if len(r.restarts) != 1 || r.restarts[0] != "puente" {
		t.Fatalf("restarts %v", r.restarts)
	}
	if st := r.a.loadState(); st.Installed != "v1.0.0" {
		t.Fatalf("state %+v", st)
	}
	// Same version again: nothing to do, nothing downloaded.
	if got, _ := r.a.Check(context.Background()); got != "up to date (v1.0.0)" || r.g.fetches != 1 {
		t.Fatalf("second check %q fetches=%d", got, r.g.fetches)
	}
}

func TestRejectsAReleaseSignedWithAnotherKey(t *testing.T) {
	r := newRig(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	r.g.publish(other, "v1.0.0", []byte("evil"), []byte("evil"))
	if _, err := r.a.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("err = %v", err)
	}
	if r.binary(t) != "old binary" || len(r.restarts) != 0 || r.g.fetches != 0 {
		t.Fatal("an unsigned release must change nothing and download nothing")
	}
}

func TestRejectsAFileWhoseHashDoesNotMatch(t *testing.T) {
	r := newRig(t)
	r.g.publish(r.key, "v1.0.0", []byte("new binary"), []byte("swapped binary"))
	if _, err := r.a.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
	if r.binary(t) != "old binary" || len(r.restarts) != 0 {
		t.Fatal("a corrupted download must not be installed")
	}
	if _, err := os.Stat(r.bin + ".new"); !os.IsNotExist(err) {
		t.Fatal("the bad download must be removed")
	}
}

func TestIgnoresOlderVersions(t *testing.T) {
	r := newRig(t)
	r.a.saveState(State{Installed: "v2.0.0"})
	r.g.publish(r.key, "v1.9.9", []byte("old"), []byte("old"))
	if got, err := r.a.Check(context.Background()); err != nil || got != "up to date (v2.0.0)" {
		t.Fatalf("Check = %q, %v", got, err)
	}
	if r.binary(t) != "old binary" || r.g.fetches != 0 {
		t.Fatal("a downgrade must never be installed")
	}
}

func TestRollsBackAnUnhealthyRelease(t *testing.T) {
	r := newRig(t)
	r.healthy = false
	r.g.publish(r.key, "v1.0.0", []byte("broken"), []byte("broken"))
	if _, err := r.a.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v", err)
	}
	if r.binary(t) != "old binary" {
		t.Fatal("the previous binary must be restored")
	}
	if len(r.restarts) != 2 {
		t.Fatalf("restart new, then restart old: %v", r.restarts)
	}
	st := r.a.loadState()
	if st.Installed != "" || len(st.Failed) != 1 || st.Failed[0] != "v1.0.0" {
		t.Fatalf("state %+v", st)
	}
	r.healthy = true
	if got, err := r.a.Check(context.Background()); err != nil || got != "skipping v1.0.0: it failed before" || r.g.fetches != 1 {
		t.Fatalf("failed version retried: %q %v fetches=%d", got, err, r.g.fetches)
	}
}

func TestRejectsServicesOutsideTheList(t *testing.T) {
	r := newRig(t)
	m, _ := json.Marshal(Manifest{Version: "v1.0.0", Files: []File{{Service: "../../bin", URL: r.g.srv.URL + "/dl/x",
		SHA256: strings.Repeat("a", 64), Size: 3}}})
	r.g.manifest, r.g.sig = m, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(r.key, m)))
	if _, err := r.a.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "not updatable") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoReleaseYetIsNotAnError(t *testing.T) {
	r := newRig(t)
	r.g.noRel = true
	if got, err := r.a.Check(context.Background()); err != nil || got != "no release published" {
		t.Fatalf("Check = %q, %v", got, err)
	}
}

func TestVersionOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.0.1", "v1.0.0", true}, {"1.10.0", "v1.9.9", true}, {"v1.0.0", "v1.0.0", false}, {"v0.9.0", "", true}, {"bad", "", false}, {"v1.01.0", "", false}} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

// A reboot between the swap and the state write leaves a pending install:
// the next check restores the backup and remembers the version as failed,
// instead of copying the new binary over the good backup (review T5).
func TestRecoversAnInstallInterruptedByAReboot(t *testing.T) {
	r := newRig(t)
	os.WriteFile(r.bin+".ota-bak", []byte("old binary"), 0o755)
	os.WriteFile(r.bin, []byte("crashing new binary"), 0o755)
	r.a.saveState(State{Installed: "v0.1.0", Pending: "v1.0.0", PendingServices: []string{"puente"}})
	r.g.publish(r.key, "v1.0.0", []byte("crashing new binary"), []byte("crashing new binary"))
	got, err := r.a.Check(context.Background())
	if err != nil || got != "skipping v1.0.0: it failed before" {
		t.Fatalf("Check = %q, %v", got, err)
	}
	if r.binary(t) != "old binary" {
		t.Fatal("the interrupted install must be rolled back to the backup")
	}
	st := r.a.loadState()
	if st.Pending != "" || st.Installed != "v0.1.0" || len(st.Failed) != 1 || st.Failed[0] != "v1.0.0" {
		t.Fatalf("state %+v", st)
	}
	if len(r.restarts) != 1 || r.g.fetches != 0 {
		t.Fatalf("restarts=%v fetches=%d", r.restarts, r.g.fetches)
	}
}

// The pending marker is written before the binary is swapped.
func TestMarksTheInstallPendingBeforeSwapping(t *testing.T) {
	r := newRig(t)
	var seen State
	r.a.Restart = func(string) error { seen = r.a.loadState(); return nil }
	r.g.publish(r.key, "v1.0.0", []byte("new binary"), []byte("new binary"))
	if _, err := r.a.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen.Pending != "v1.0.0" {
		t.Fatalf("state during restart %+v, want pending v1.0.0", seen)
	}
	if st := r.a.loadState(); st.Pending != "" || st.Installed != "v1.0.0" {
		t.Fatalf("final state %+v", st)
	}
}

// A damaged state file must not turn into "nothing installed", which would
// allow downgrades and retry failed versions (review T5).
func TestRefusesToInstallWithACorruptStateFile(t *testing.T) {
	r := newRig(t)
	os.MkdirAll(filepath.Dir(r.a.StateFile), 0o755)
	os.WriteFile(r.a.StateFile, []byte(`{"installed":"v2.`), 0o644)
	r.g.publish(r.key, "v1.0.0", []byte("new binary"), []byte("new binary"))
	if _, err := r.a.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v", err)
	}
	if r.binary(t) != "old binary" || r.g.fetches != 0 {
		t.Fatal("nothing may be installed while the state is unreadable")
	}
}

func TestOnlyOneCheckRunsAtATime(t *testing.T) {
	r := newRig(t)
	unlock, err := r.a.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	r.g.publish(r.key, "v1.0.0", []byte("new binary"), []byte("new binary"))
	if _, err := r.a.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "another check") {
		t.Fatalf("err = %v", err)
	}
}

// A binary that crashes at start makes the supervisor restart it again and
// again; health must fail fast, before the supervisor gives up after 10
// failures in 10 minutes, so the rollback still gets restarted (review T5).
func TestHealthFailsFastOnACrashLoop(t *testing.T) {
	pid := 100
	start := time.Now()
	ok := waitHealthy(context.Background(), healthWait{
		Up:   func() bool { return false },
		PIDs: func() []int { pid++; return []int{pid} }, // a new process each poll
		Poll: time.Millisecond, Deadline: time.Second, Settle: time.Millisecond,
		MaxPIDs: 3,
	})
	if ok || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("ok=%v after %v, want a fast failure", ok, time.Since(start))
	}
	n := 0
	ok = waitHealthy(context.Background(), healthWait{
		Up:   func() bool { n++; return n > 2 },
		PIDs: func() []int { return []int{7} },
		Poll: time.Millisecond, Deadline: time.Second, Settle: time.Millisecond, MaxPIDs: 3,
	})
	if !ok {
		t.Fatal("a service that comes up and stays up is healthy")
	}
}
