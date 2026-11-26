package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// pauseRadio counts pause and resume calls on top of fakeRadio.
type pauseRadio struct {
	fakeRadio
	mu2             sync.Mutex
	pauses, resumes int
}

func (p *pauseRadio) Pause() error  { p.mu2.Lock(); p.pauses++; p.mu2.Unlock(); return nil }
func (p *pauseRadio) Resume() error { p.mu2.Lock(); p.resumes++; p.mu2.Unlock(); return nil }

func controlServer(t *testing.T) (*server, *fakeVoice) {
	t.Helper()
	old := *svc
	t.Cleanup(func() { svc.Reader.Stop(); *svc = old; pausedPlayer = false })
	v := &fakeVoice{}
	svc.Reader = &Reader{Voice: &slowVoice{dur: 30 * time.Millisecond}, Poll: 2 * time.Millisecond, Idle: 5 * time.Millisecond, StartWait: 5 * time.Millisecond}
	hits := 0
	up := newUpstream(t, `{"choices":[{"message":{"role":"assistant","content":"no debería llamarse"}}]}`, &hits)
	t.Cleanup(up.Close)
	return &server{upstream: up.URL, api: &fakeAPI{}, client: up.Client()}, v
}

func say(s *server, text string) string {
	return s.answer(context.Background(), inbound{Messages: []chatMessage{{Role: "user", Content: text}}}, "Bearer k")
}

var longRead = Reading{Title: "Lectura", Pieces: []ReadPiece{{Text: "Uno."}, {Text: "Dos."}, {Text: "Tres."}, {Text: "Cuatro."}}}

func TestStopEndsEverythingAndForgets(t *testing.T) {
	for _, word := range []string{"para", "Stop", "¡Para!"} {
		s, _ := controlServer(t)
		svc.Reader.Start(longRead, 0)
		if got := say(s, word); got != "Listo." {
			t.Fatalf("%q -> %q", word, got)
		}
		if svc.Reader.Active() {
			t.Fatalf("%q must stop the reading", word)
		}
		if got := say(s, "continuar"); got != "No hay nada en pausa." {
			t.Fatalf("after %q nothing is left to resume: %q", word, got)
		}
	}
}

func TestPauseAndResumeReading(t *testing.T) {
	for _, pair := range [][2]string{{"pausa", "continuar"}, {"pon pausa", "reanudar"}, {"pausar", "continúa"}, {"pausa", "reanuda"}, {"pausa", "sigue"}, {"pausa", "Sigue."}} {
		s, _ := controlServer(t)
		svc.Reader.Start(longRead, 0)
		waitFor(t, func() bool { return svc.Reader.Active() })
		if got := say(s, pair[0]); got != "En pausa." {
			t.Fatalf("%q -> %q", pair[0], got)
		}
		if svc.Reader.Active() {
			t.Fatalf("%q must stop the voice", pair[0])
		}
		if got := say(s, pair[1]); got != "Sigo." {
			t.Fatalf("%q -> %q", pair[1], got)
		}
		if !svc.Reader.Active() {
			t.Fatalf("%q must resume the reading", pair[1])
		}
	}
}

func TestPauseAndResumeMusic(t *testing.T) {
	s, _ := controlServer(t)
	r := &pauseRadio{}
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	svc.Player.Start("x", []Track{{Title: "a", URL: "u1"}}, false)
	if got := say(s, "pausa"); got != "En pausa." || r.pauses != 1 {
		t.Fatalf("pause music: %q pauses=%d", got, r.pauses)
	}
	if got := say(s, "reanudar"); got != "Sigo." || r.resumes != 1 {
		t.Fatalf("resume music: %q resumes=%d", got, r.resumes)
	}
	if !svc.Player.Active() {
		t.Fatal("the queue must still be there")
	}
}

func TestPauseWithNothingPlaying(t *testing.T) {
	s, _ := controlServer(t)
	svc.Player = nil
	if got := say(s, "pausa"); got != "No hay nada sonando para pausar." {
		t.Fatalf("pause: %q", got)
	}
	if got := say(s, "reanudar"); got != "No hay nada en pausa." {
		t.Fatalf("resume: %q", got)
	}
}
