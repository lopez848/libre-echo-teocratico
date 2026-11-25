package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusProblems(t *testing.T) {
	status := "schema=1\nenabled=1\nstate=running\nservice.puente=running\nservice.tts-local=exited:1\nservice.voice-es=exited:0\n"
	got := statusProblems(parseStatus(status))
	if len(got) != 1 || got[0].Key != "service:tts-local" || !strings.Contains(got[0].Text, "tts-local") {
		t.Fatalf("got %+v", got)
	}
	if p := statusProblems(parseStatus("state=stopped\n")); len(p) != 1 || p[0].Key != "local:state" {
		t.Fatalf("stopped supervisor: %+v", p)
	}
}

func TestRestartDetectedFromFailureCounter(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "puente"), 0o755)
	write := func(n string) { os.WriteFile(filepath.Join(dir, "puente", "failures"), []byte(n+"\n"), 0o644) }
	r := &restartWatch{Dir: dir}
	write("2")
	if p := r.Check(); len(p) != 0 {
		t.Fatalf("first read must only set the baseline: %+v", p)
	}
	write("3")
	p := r.Check()
	if len(p) != 1 || p[0].Key != "restart:puente" || !p[0].Instant {
		t.Fatalf("got %+v", p)
	}
	if p := r.Check(); len(p) != 0 {
		t.Fatalf("same counter must not repeat: %+v", p)
	}
}

func TestLogScannerOnlyNewLinesAndTruncation(t *testing.T) {
	f := filepath.Join(t.TempDir(), "puente.log")
	os.WriteFile(f, []byte("2026/11/25 01:00:00 upstream error: old\n"), 0o644)
	s := &logScanner{Path: f}
	if l := s.Scan(); len(l) != 0 {
		t.Fatalf("history must be skipped: %q", l)
	}
	fh, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
	fh.WriteString("2026/11/25 01:01:00 turn done in 500ms\n2026/11/25 01:01:05 daily text: timeout\n")
	fh.Close()
	if l := s.Scan(); len(l) != 2 {
		t.Fatalf("got %q", l)
	}
	os.WriteFile(f, []byte("2026/11/25 02:00:00 wyoming: broken pipe\n"), 0o644)
	if l := s.Scan(); len(l) != 1 || !strings.Contains(l[0], "wyoming") {
		t.Fatalf("after truncation got %q", l)
	}
}

func TestLogProblem(t *testing.T) {
	cases := map[string]string{
		"2026/11/25 01:01:05 upstream error: Post \"https://x\": timeout": "log:upstream error",
		"2026/11/25 01:01:05 daily text: status 503":                      "log:daily text",
		"2026/11/25 01:01:05 cloud tts failed, using local voice: eof":    "log:cloud tts failed",
		"panic: runtime error: index out of range":                        "log:panic",
	}
	for line, key := range cases {
		p, ok := logProblem(line)
		if !ok || p.Key != key || !p.Instant {
			t.Errorf("%q -> %+v %v, want %s", line, p, ok, key)
		}
	}
	for _, line := range []string{
		"2026/11/25 01:01:00 turn done in 500ms",
		"2026/11/25 01:01:00 player: stopped early (3s of 200s), dropping queue",
		"2026/11/25 01:01:00 tool set_timer {\"seconds\":60}",
	} {
		if p, ok := logProblem(line); ok {
			t.Errorf("%q must not be a problem: %+v", line, p)
		}
	}
}

func TestWatcherRechecksBeforeAlerting(t *testing.T) {
	w := newWatcher(90*time.Second, time.Hour)
	t0 := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	down := []Problem{{Key: "service:puente", Text: "El servicio puente no está corriendo (exited:1)."}}
	if a := w.Tick(t0, down); len(a) != 0 {
		t.Fatalf("must wait for the recheck: %+v", a)
	}
	if a := w.Tick(t0.Add(30*time.Second), down); len(a) != 0 {
		t.Fatalf("still inside the recheck window: %+v", a)
	}
	a := w.Tick(t0.Add(90*time.Second), down)
	if len(a) != 1 || a[0].Recovered || !strings.Contains(a[0].Message, "Sigue fallando") {
		t.Fatalf("got %+v", a)
	}
	// Recovers: one recovery notice.
	a = w.Tick(t0.Add(120*time.Second), nil)
	if len(a) != 1 || !a[0].Recovered {
		t.Fatalf("got %+v", a)
	}
}

func TestWatcherSelfRecoveredProblem(t *testing.T) {
	w := newWatcher(90*time.Second, time.Hour)
	t0 := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	w.Tick(t0, []Problem{{Key: "tcp:wyoming", Text: "La voz de la nube no responde."}})
	a := w.Tick(t0.Add(90*time.Second), nil)
	if len(a) != 1 || !a[0].Recovered || !strings.Contains(a[0].Message, "se recuperó solo") {
		t.Fatalf("got %+v", a)
	}
}

func TestWatcherRateLimitsInstantProblems(t *testing.T) {
	w := newWatcher(90*time.Second, time.Hour)
	t0 := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	p := []Problem{{Key: "log:daily text", Text: "daily text: timeout", Instant: true}}
	if a := w.Tick(t0, p); len(a) != 1 {
		t.Fatalf("first instant problem must alert: %+v", a)
	}
	if a := w.Tick(t0.Add(10*time.Minute), p); len(a) != 0 {
		t.Fatalf("repeat within the hour must be muted: %+v", a)
	}
	a := w.Tick(t0.Add(61*time.Minute), p)
	if len(a) != 1 || !strings.Contains(a[0].Message, "1 vez más") {
		t.Fatalf("after the hour, alert with the muted count: %+v", a)
	}
}

func TestNtfySend(t *testing.T) {
	var path, title, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, title = r.URL.Path, r.Header.Get("Title")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, `{"id":"abc"}`)
	}))
	defer srv.Close()
	n := &ntfy{Server: srv.URL, Topic: "topic-x", HTTP: srv.Client()}
	if err := n.Send(Alert{Key: "service:puente", Message: "Sigue fallando: puente."}); err != nil {
		t.Fatal(err)
	}
	if path != "/topic-x" || title == "" || body != "Sigue fallando: puente." {
		t.Fatalf("path=%q title=%q body=%q", path, title, body)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer bad.Close()
	n.Server = bad.URL
	if err := n.Send(Alert{Message: "x"}); err == nil {
		t.Fatal("a non-2xx answer must be an error")
	}
}

func TestEventLogRotates(t *testing.T) {
	dir := t.TempDir()
	e := &eventLog{Path: filepath.Join(dir, "events.jsonl"), Max: 200}
	for i := 0; i < 10; i++ {
		e.Add(Event{Time: time.Unix(0, 0), Key: "log:panic", Status: "detected", Detail: strings.Repeat("x", 40)})
	}
	if st, err := os.Stat(e.Path); err != nil || st.Size() > 200 {
		t.Fatalf("current file must stay under the cap: %v %v", st, err)
	}
	if _, err := os.Stat(e.Path + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
}

func TestLoadConfig(t *testing.T) {
	f := filepath.Join(t.TempDir(), "vigia.conf")
	os.WriteFile(f, []byte("# comment\ntopic=abc123\nserver=https://ntfy.example\n"), 0o600)
	c, err := loadConfig(f)
	if err != nil || c["topic"] != "abc123" || c["server"] != "https://ntfy.example" {
		t.Fatalf("%v %v", c, err)
	}
}
