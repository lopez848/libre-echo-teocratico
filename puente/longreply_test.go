package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeVoice plays the role of ttsd: it reports speaking while busyUntil is
// in the future and records what the bridge asked it to say.
type fakeVoice struct {
	mu        sync.Mutex
	busyUntil time.Time
	spoken    []string
	startedAt []time.Time
}

func (f *fakeVoice) Speak(text string) error {
	f.mu.Lock()
	f.spoken = append(f.spoken, text)
	f.startedAt = append(f.startedAt, time.Now())
	f.busyUntil = time.Now().Add(10 * time.Millisecond)
	f.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	return nil
}

func (f *fakeVoice) Speaking() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Before(f.busyUntil), nil
}

func (f *fakeVoice) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.spoken...)
}

func longText(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "Esta es la oración número %d del texto diario, con algo de relleno para que pese. ", i)
	}
	return strings.TrimSpace(b.String())
}

func TestSplitForAgentdKeepsWholeSentencesUnderTheLimit(t *testing.T) {
	text := longText(14) // ~1200 bytes
	head, tail := splitForAgentd(text, 560)
	if n := len(Speakable(head)); n > 560 || n < 300 {
		t.Fatalf("head is %d bytes", n)
	}
	if !strings.HasSuffix(head, ".") || len(tail) == 0 {
		t.Fatalf("head must end a sentence and leave a tail: %q / %d", head, len(tail))
	}
	if got := head + " " + strings.Join(tail, " "); got != text {
		t.Fatalf("splitting changed the text:\n%q\n%q", got, text)
	}
	if h, tl := splitForAgentd("Hola. ¿Qué tal?", 560); h != "Hola. ¿Qué tal?" || tl != nil {
		t.Fatalf("short replies go whole to agentd: %q %v", h, tl)
	}
	// One very long sentence still fits: it is cut at a word boundary.
	h, tl := splitForAgentd(strings.Repeat("palabra ", 150), 560)
	if len(Speakable(h)) > 560 || len(tl) == 0 {
		t.Fatalf("long sentence: head %d bytes, tail %d", len(Speakable(h)), len(tl))
	}
}

func TestTailSpeakerWaitsForSilenceThenSpeaksInOrder(t *testing.T) {
	v := &fakeVoice{busyUntil: time.Now().Add(80 * time.Millisecond)} // agentd still talking
	ts := &tailSpeaker{Voice: v, Poll: 5 * time.Millisecond, Idle: 30 * time.Millisecond, StartWait: time.Second}
	silentAt := time.Now().Add(80 * time.Millisecond)
	ts.Start([]string{"Segunda parte.", "Tercera parte."})
	waitFor(t, func() bool { return len(v.said()) == 2 })
	if s := v.said(); s[0] != Speakable("Segunda parte.") || s[1] != Speakable("Tercera parte.") {
		t.Fatalf("spoken %q", s)
	}
	if v.startedAt[0].Before(silentAt.Add(30 * time.Millisecond)) {
		t.Fatal("the tail must wait until the head finished speaking")
	}
}

func TestTailSpeakerCancel(t *testing.T) {
	v := &fakeVoice{busyUntil: time.Now().Add(60 * time.Millisecond)}
	ts := &tailSpeaker{Voice: v, Poll: 5 * time.Millisecond, Idle: 20 * time.Millisecond, StartWait: time.Second}
	ts.Start([]string{"No debe sonar."})
	ts.Cancel()
	time.Sleep(150 * time.Millisecond)
	if s := v.said(); len(s) != 0 {
		t.Fatalf("a cancelled tail must not speak: %q", s)
	}
}

func TestChatSendsHeadToAgentdAndSpeaksTheRest(t *testing.T) {
	text := longText(14)
	b, _ := json.Marshal(text)
	hits := 0
	up := newUpstream(t, `{"choices":[{"message":{"role":"assistant","content":`+string(b)+`}}]}`, &hits)
	defer up.Close()
	v := &fakeVoice{}
	s := &server{upstream: up.URL, api: &fakeAPI{}, client: up.Client(),
		tail: &tailSpeaker{Voice: v, Poll: 5 * time.Millisecond, Idle: 10 * time.Millisecond, StartWait: 20 * time.Millisecond}}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"lee el texto diario"}]}`))
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	s.chat(rec, req)
	var sent strings.Builder
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if p, ok := strings.CutPrefix(line, "data: {"); ok && json.Unmarshal([]byte("{"+p), &ev) == nil && len(ev.Choices) > 0 {
			sent.WriteString(ev.Choices[0].Delta.Content)
		}
	}
	if n := sent.Len(); n == 0 || n > agentdReplyMax {
		t.Fatalf("agentd got %d bytes, limit %d", n, agentdReplyMax)
	}
	waitFor(t, func() bool {
		return len(v.said()) > 0 && strings.Contains(strings.Join(v.said(), " "), Speakable("número 14"))
	})
	all := sent.String() + " " + strings.Join(v.said(), " ")
	if all != Speakable(text) {
		t.Fatalf("head + tail must be the whole reply:\n%q\n%q", all, Speakable(text))
	}
	// A new turn cancels a pending tail.
	s.tail.Start([]string{"Pendiente."})
	s.chat(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"para"}]}`)))
	time.Sleep(80 * time.Millisecond)
	if strings.Contains(strings.Join(v.said(), " "), "Pendiente") {
		t.Fatal("a new turn must cancel the pending tail")
	}
}
