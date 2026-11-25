package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Problem is one failure seen by a check. Key is a stable signature used to
// group repeats; Instant problems (log errors, restarts) have no state to
// recheck, so they alert right away.
type Problem struct {
	Key     string
	Text    string // goes to the phone: never raw log content
	Detail  string // stays in the local event log; defaults to Text
	Instant bool
}

func (p Problem) detail() string {
	if p.Detail != "" {
		return p.Detail
	}
	return p.Text
}

// Alert is a message for the owner's phone.
type Alert struct {
	Key       string
	Message   string
	Recovered bool
}

// Event is one line of the on-device failure history the PC skill reads.
type Event struct {
	Time   time.Time `json:"time"`
	Key    string    `json:"key"`
	Status string    `json:"status"` // detected, alerted, muted, recovered
	Detail string    `json:"detail"`
}

func parseStatus(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			m[k] = v
		}
	}
	return m
}

// statusProblems reads the local service supervisor status. One-shot
// services that ended with exit code 0 are healthy.
func statusProblems(st map[string]string) []Problem {
	var out []Problem
	if v, ok := st["state"]; ok && v != "running" {
		out = append(out, Problem{Key: "local:state", Text: "El supervisor de servicios locales está en estado " + v + "."})
	}
	var keys []string
	for k := range st {
		if strings.HasPrefix(k, "service.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := st[k]
		if v == "running" || v == "exited:0" {
			continue
		}
		name := strings.TrimPrefix(k, "service.")
		out = append(out, Problem{Key: "service:" + name, Text: fmt.Sprintf("El servicio %s no está corriendo (%s).", name, v)})
	}
	return out
}

// restartWatch notices supervisor restarts through each service's
// failures counter, so a crash between two checks is not missed.
type restartWatch struct {
	Dir  string
	last map[string]int
}

func (r *restartWatch) Check() []Problem {
	if r.last == nil {
		r.last = map[string]int{}
	}
	files, _ := filepath.Glob(filepath.Join(r.Dir, "*", "failures"))
	sort.Strings(files)
	var out []Problem
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		svc := filepath.Base(filepath.Dir(f))
		if prev, ok := r.last[svc]; ok && n > prev {
			out = append(out, Problem{Key: "restart:" + svc, Text: "El servicio " + svc + " se cayó y el supervisor lo reinició.", Instant: true})
		}
		r.last[svc] = n
	}
	return out
}

// logScanner returns the complete lines appended to a log since the last
// scan. The first scan skips history; a shorter file means it was rotated.
type logScanner struct {
	Path    string
	off     int64
	started bool
}

func (s *logScanner) Scan() []string {
	st, err := os.Stat(s.Path)
	if err != nil {
		return nil
	}
	if !s.started {
		s.started, s.off = true, st.Size()
		return nil
	}
	if st.Size() < s.off {
		s.off = 0
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(s.off, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil
	}
	end := strings.LastIndexByte(string(b), '\n')
	if end < 0 {
		return nil
	}
	s.off += int64(end + 1)
	return strings.Split(string(b[:end]), "\n")
}

// logPatterns are the bridge's error lines (puente logf calls). Log lines
// can carry what the owner said, so alerts only name the kind of error and
// the full line stays in the on-device event log.
var logPatterns = []string{
	"upstream error:", "device error:", "daily text:", "jw collection:",
	"busplayer:", "wyoming:", "cloud tts failed", "local voice failed",
	"player:", "bad tool args", "panic:",
}

var logLabels = map[string]string{
	"upstream error":     "falló la llamada al modelo de IA",
	"device error":       "falló una orden al Echo",
	"daily text":         "no se pudo traer el texto diario",
	"jw collection":      "no se pudo traer la música de jw.org",
	"busplayer":          "falló la reproducción de audio",
	"wyoming":            "falló la voz de la nube",
	"cloud tts failed":   "falló la voz de la nube y usó la voz local",
	"local voice failed": "falló la voz local",
	"player":             "falló el reproductor",
	"bad tool args":      "el modelo mandó una orden mal formada",
	"panic":              "el puente se cayó (panic)",
}

func logProblem(line string) (Problem, bool) {
	msg := strings.TrimSpace(line)
	// Drop the log package timestamp "2006/01/02 15:04:05 ".
	if len(msg) > 20 && msg[4] == '/' && msg[7] == '/' && msg[13] == ':' {
		msg = msg[20:]
	}
	if strings.HasPrefix(msg, "player: stopped early") {
		return Problem{}, false // the owner skipped a song
	}
	for _, p := range logPatterns {
		if strings.HasPrefix(msg, p) {
			if len(msg) > 200 {
				msg = msg[:200]
			}
			kind := strings.TrimSuffix(p, ":")
			text := "Error en el puente: " + logLabels[kind] + ". El detalle está en el registro del Echo."
			return Problem{Key: "log:" + kind, Text: text, Detail: msg, Instant: true}, true
		}
	}
	return Problem{}, false
}

type pending struct {
	first   time.Time
	text    string
	alerted bool
	muted   bool // reached the recheck inside the rate window: no alerts
}

// watcher turns check results into alerts: state problems are rechecked
// before alerting, instant problems alert at once, and the same signature
// alerts at most once per rate window.
type watcher struct {
	recheck, rate time.Duration
	pend          map[string]*pending
	lastSent      map[string]time.Time
	muted         map[string]int
	OnEvent       func(Event)
}

func newWatcher(recheck, rate time.Duration) *watcher {
	return &watcher{recheck: recheck, rate: rate, pend: map[string]*pending{}, lastSent: map[string]time.Time{}, muted: map[string]int{}}
}

func (w *watcher) event(now time.Time, key, status, detail string) {
	if w.OnEvent != nil {
		w.OnEvent(Event{Time: now, Key: key, Status: status, Detail: detail})
	}
}

// limited reports whether key already alerted inside the rate window.
func (w *watcher) limited(now time.Time, key string) bool {
	last, ok := w.lastSent[key]
	return ok && now.Sub(last) < w.rate
}

func (w *watcher) send(now time.Time, key, msg string, recovered bool) Alert {
	if n := w.muted[key]; n > 0 {
		unit := "veces"
		if n == 1 {
			unit = "vez"
		}
		msg += fmt.Sprintf(" (pasó %d %s más en la última hora)", n, unit)
		w.muted[key] = 0
	}
	w.lastSent[key] = now
	status := "alerted"
	if recovered {
		status = "recovered"
	}
	w.event(now, key, status, msg)
	return Alert{Key: key, Message: msg, Recovered: recovered}
}

func (w *watcher) Tick(now time.Time, probs []Problem) []Alert {
	var out []Alert
	current := map[string]bool{}
	for _, p := range probs {
		if p.Instant {
			w.event(now, p.Key, "detected", p.detail())
			if w.limited(now, p.Key) {
				w.muted[p.Key]++
				w.event(now, p.Key, "muted", p.detail())
				continue
			}
			out = append(out, w.send(now, p.Key, "Falla: "+p.Text, false))
			continue
		}
		current[p.Key] = true
		pd, ok := w.pend[p.Key]
		if !ok {
			w.pend[p.Key] = &pending{first: now, text: p.Text}
			w.event(now, p.Key, "detected", p.detail())
			continue
		}
		if !pd.alerted && now.Sub(pd.first) >= w.recheck {
			pd.alerted = true
			if w.limited(now, p.Key) {
				pd.muted = true
				w.muted[p.Key]++
				w.event(now, p.Key, "muted", pd.text)
				continue
			}
			out = append(out, w.send(now, p.Key, "Sigue fallando: "+pd.text, false))
		}
	}
	keys := make([]string, 0, len(w.pend))
	for k := range w.pend {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if current[k] {
			continue
		}
		pd := w.pend[k]
		delete(w.pend, k)
		if pd.muted {
			w.event(now, k, "recovered", "Se recuperó (aviso silenciado): "+pd.text)
			continue
		}
		if pd.alerted {
			out = append(out, w.send(now, k, "Ya funciona otra vez. El problema era: "+pd.text, true))
			continue
		}
		if w.limited(now, k) {
			w.muted[k]++
			w.event(now, k, "muted", pd.text)
			continue
		}
		out = append(out, w.send(now, k, "Falló y se recuperó solo. El problema era: "+pd.text, true))
	}
	return out
}

// ntfy sends alerts to an ntfy topic the owner's phone subscribes to.
type ntfy struct {
	Server, Topic string
	HTTP          *http.Client
}

func (n *ntfy) Send(a Alert) error {
	req, err := http.NewRequest("POST", strings.TrimRight(n.Server, "/")+"/"+n.Topic, strings.NewReader(a.Message))
	if err != nil {
		return err
	}
	if a.Recovered {
		req.Header.Set("Title", "Mia: se recupero")
		req.Header.Set("Tags", "white_check_mark")
	} else {
		req.Header.Set("Title", "Mia: falla en el Echo")
		req.Header.Set("Tags", "warning")
		req.Header.Set("Priority", "high")
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy answered %s", resp.Status)
	}
	return nil
}

// outbox keeps alerts that could not be sent (no network, ntfy down) and
// retries them, oldest first, on the next flush. It holds the newest Max.
type outbox struct {
	Max     int
	pending []Alert
}

// Flush queues the new alerts and sends everything in order, stopping at
// the first failure so the rest waits for the next flush.
func (o *outbox) Flush(send func(Alert) error, alerts []Alert) []error {
	o.pending = append(o.pending, alerts...)
	if o.Max > 0 && len(o.pending) > o.Max {
		o.pending = o.pending[len(o.pending)-o.Max:]
	}
	for len(o.pending) > 0 {
		if err := send(o.pending[0]); err != nil {
			return []error{err}
		}
		o.pending = o.pending[1:]
	}
	return nil
}

// eventLog appends JSON lines and keeps one rotated file.
type eventLog struct {
	Path string
	Max  int64
}

func (e *eventLog) Add(ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if st, err := os.Stat(e.Path); err == nil && st.Size()+int64(len(b)) > e.Max {
		os.Rename(e.Path, e.Path+".1")
	}
	os.MkdirAll(filepath.Dir(e.Path), 0o755)
	f, err := os.OpenFile(e.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(b)
}

func loadConfig(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			c[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return c, sc.Err()
}
