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

func TestLogProblemKeepsSpeechOffThePhone(t *testing.T) {
	line := `2026/11/25 01:01:05 bad tool args for set_reminder: "{label:'llamar a Ana al 555-1234'"`
	p, ok := logProblem(line)
	if !ok {
		t.Fatal("must be a problem")
	}
	if strings.Contains(p.Text, "555") || strings.Contains(p.Text, "Ana") {
		t.Fatalf("alert text must not carry the logged content: %q", p.Text)
	}
	if !strings.Contains(p.Detail, "555-1234") {
		t.Fatalf("the local detail must keep the full line: %q", p.Detail)
	}
	w := newWatcher(90*time.Second, time.Hour)
	var evs []Event
	w.OnEvent = func(e Event) { evs = append(evs, e) }
	a := w.Tick(time.Unix(0, 0), []Problem{p})
	if len(a) != 1 || strings.Contains(a[0].Message, "555") {
		t.Fatalf("alert: %+v", a)
	}
	if len(evs) == 0 || evs[0].Status != "detected" || !strings.Contains(evs[0].Detail, "555-1234") {
		t.Fatalf("detected event must keep the detail: %+v", evs)
	}
}

func TestWatcherRateLimitsFlappingService(t *testing.T) {
	w := newWatcher(90*time.Second, time.Hour)
	t0 := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	down := []Problem{{Key: "service:puente", Text: "El servicio puente no está corriendo (exited:1)."}}
	w.Tick(t0, down)
	if a := w.Tick(t0.Add(90*time.Second), down); len(a) != 1 {
		t.Fatalf("first lasting failure must alert: %+v", a)
	}
	if a := w.Tick(t0.Add(120*time.Second), nil); len(a) != 1 || !a[0].Recovered {
		t.Fatalf("first recovery must alert: %+v", a)
	}
	// Flaps again inside the hour: no new alerts, neither failure nor recovery.
	w.Tick(t0.Add(150*time.Second), down)
	if a := w.Tick(t0.Add(240*time.Second), down); len(a) != 0 {
		t.Fatalf("repeat inside the hour must be muted: %+v", a)
	}
	if a := w.Tick(t0.Add(270*time.Second), nil); len(a) != 0 {
		t.Fatalf("muted failure must recover silently: %+v", a)
	}
	// After the hour it alerts again and reports the muted repeat.
	t1 := t0.Add(2 * time.Hour)
	w.Tick(t1, down)
	a := w.Tick(t1.Add(90*time.Second), down)
	if len(a) != 1 || !strings.Contains(a[0].Message, "más en la última hora") {
		t.Fatalf("after the hour: %+v", a)
	}
}

func TestOutboxRetriesFailedSends(t *testing.T) {
	var sent []string
	fail := true
	send := func(a Alert) error {
		if fail {
			return io.ErrUnexpectedEOF
		}
		sent = append(sent, a.Message)
		return nil
	}
	o := &outbox{Max: 3}
	if errs := o.Flush(send, []Alert{{Message: "uno"}, {Message: "dos"}}); len(errs) != 1 {
		t.Fatalf("one error per flush when the server is down: %v", errs)
	}
	fail = false
	o.Flush(send, []Alert{{Message: "tres"}})
	if strings.Join(sent, ",") != "uno,dos,tres" {
		t.Fatalf("queued alerts must go first, in order: %v", sent)
	}
	if errs := o.Flush(send, nil); len(errs) != 0 || len(sent) != 3 {
		t.Fatalf("nothing left to send: %v %v", errs, sent)
	}
	fail = true
	o.Flush(send, []Alert{{Message: "a"}, {Message: "b"}, {Message: "c"}, {Message: "d"}})
	fail = false
	sent = nil
	o.Flush(send, nil)
	if strings.Join(sent, ",") != "b,c,d" {
		t.Fatalf("queue keeps only the newest Max alerts: %v", sent)
	}
}

// A restart someone announced (a deploy or an OTA update) is recorded but
// not sent to the phone (owner, 2026-11-26).
func TestPlannedRestartIsSilent(t *testing.T) {
	w := newWatcher(90*time.Second, time.Hour)
	t0 := time.Date(2026, 11, 26, 22, 0, 0, 0, time.UTC)
	until := t0.Add(3 * time.Minute)
	w.Planned = func(now time.Time) map[string]bool {
		return map[string]bool{"puente": now.Before(until)}
	}
	var events []Event
	w.OnEvent = func(e Event) { events = append(events, e) }
	down := []Problem{
		{Key: "restart:puente", Text: "El servicio puente se cayó y el supervisor lo reinició.", Instant: true},
		{Key: "service:puente", Text: "El servicio puente no está corriendo (exited:143)."},
		{Key: "http:puente", Text: "El puente no responde."},
		{Key: "tcp:wyoming", Text: "La voz de la nube no responde."},
	}
	if a := w.Tick(t0, down); len(a) != 0 {
		t.Fatalf("a planned restart must not alert: %+v", a)
	}
	if a := w.Tick(t0.Add(20*time.Second), nil); len(a) != 0 {
		t.Fatalf("recovering from a planned restart must not alert: %+v", a)
	}
	planned := 0
	for _, e := range events {
		if e.Status == "planned" {
			planned++
		}
	}
	if planned == 0 {
		t.Fatal("planned restarts must still be recorded in the event log")
	}
	// Other services still alert during the window.
	if a := w.Tick(t0.Add(30*time.Second), []Problem{{Key: "restart:vigia", Text: "x", Instant: true}}); len(a) != 1 {
		t.Fatalf("another service must still alert: %+v", a)
	}
	// Still down after the window: alert as usual.
	stuck := []Problem{{Key: "service:puente", Text: "El servicio puente no está corriendo (exited:1)."}}
	w.Tick(t0.Add(2*time.Minute), stuck)
	if a := w.Tick(t0.Add(4*time.Minute), stuck); len(a) != 1 || !strings.Contains(a[0].Message, "Sigue fallando") {
		t.Fatalf("a restart that does not come back must alert after the window: %+v", a)
	}
}

func TestPlannedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "planned")
	now := time.Unix(1_800_000_000, 0)
	if got := readPlanned(path, now); len(got) != 0 {
		t.Fatalf("no file: %v", got)
	}
	os.WriteFile(path, []byte("puente 1800000100\nvigia 1799999000\nbad line\n"), 0o644)
	got := readPlanned(path, now)
	if !got["puente"] || got["vigia"] || len(got) != 1 {
		t.Fatalf("got %v, want only puente (vigia expired)", got)
	}
}
