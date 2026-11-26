package main

import (
	"strings"
	"testing"
	"time"
)

// slowVoice speaks each text for a fixed time, like a real ttsd.
type slowVoice struct {
	fakeVoice
	dur time.Duration
}

func (s *slowVoice) Speak(text string) error {
	s.mu.Lock()
	s.spoken = append(s.spoken, text)
	s.busyUntil = time.Now().Add(s.dur)
	s.mu.Unlock()
	time.Sleep(s.dur)
	return nil
}

func testReading() Reading {
	return Reading{Title: "Prueba", Pieces: []ReadPiece{
		{Text: "Introducción."}, {Para: 1, Text: "Párrafo uno."}, {Para: 2, Text: "Párrafo dos."},
		{Para: 3, Text: "Párrafo tres."}, {Para: 4, Text: "Párrafo cuatro."},
	}}
}

func newTestReader(v voice) *Reader {
	return &Reader{Voice: v, Poll: 2 * time.Millisecond, Idle: 5 * time.Millisecond, StartWait: 5 * time.Millisecond}
}

func TestReaderReadsAllPiecesInOrder(t *testing.T) {
	v := &fakeVoice{}
	r := newTestReader(v)
	r.Start(testReading(), 0)
	waitFor(t, func() bool { return len(v.said()) == 5 })
	if got := strings.Join(v.said(), " | "); got != Speakable("Introducción. | Párrafo uno. | Párrafo dos. | Párrafo tres. | Párrafo cuatro.") {
		t.Fatalf("spoken %q", got)
	}
	waitFor(t, func() bool { return !r.Active() })
	if _, _, ok := r.Resume(); ok {
		t.Fatal("a finished reading has nothing to resume")
	}
}

func TestReaderStopAndResumeFromTheSameParagraph(t *testing.T) {
	v := &slowVoice{dur: 40 * time.Millisecond}
	r := newTestReader(v)
	r.Start(testReading(), 0)
	waitFor(t, func() bool { return len(v.said()) == 3 }) // speaking "Párrafo dos."
	r.Stop()
	time.Sleep(80 * time.Millisecond)
	if n := len(v.said()); n != 3 {
		t.Fatalf("stop must end the reading: %d pieces spoken", n)
	}
	title, para, ok := r.Resume()
	if !ok || title != "Prueba" || para != 2 {
		t.Fatalf("resume = %q %d %v", title, para, ok)
	}
	waitFor(t, func() bool { return len(v.said()) == 6 })
	if got := strings.Join(v.said()[3:], " | "); got != Speakable("Párrafo dos. | Párrafo tres. | Párrafo cuatro.") {
		t.Fatalf("must repeat the interrupted paragraph and go on: %q", got)
	}
}

func TestReaderJumpToParagraph(t *testing.T) {
	v := &fakeVoice{}
	r := newTestReader(v)
	if _, ok := r.Jump(3); ok {
		t.Fatal("no reading loaded yet")
	}
	r.Start(testReading(), 0)
	r.Stop()
	if _, ok := r.Jump(9); ok {
		t.Fatal("unknown paragraph must fail")
	}
	if title, ok := r.Jump(3); !ok || title != "Prueba" {
		t.Fatalf("jump %q %v", title, ok)
	}
	waitFor(t, func() bool { return len(v.said()) > 0 && v.said()[len(v.said())-1] == Speakable("Párrafo cuatro.") })
	got := v.said()
	if i := indexOf(got, Speakable("Párrafo tres.")); i < 0 || indexOf(got[i:], Speakable("Párrafo dos.")) >= 0 {
		t.Fatalf("jump must start at paragraph 3: %q", got)
	}
}

func TestReaderSplitsLongPiecesForTheVoice(t *testing.T) {
	v := &fakeVoice{}
	r := newTestReader(v)
	long := strings.Repeat("Una oración de relleno para que el párrafo sea largo. ", 12)
	r.Start(Reading{Title: "Largo", Pieces: []ReadPiece{{Para: 1, Text: long}}}, 0)
	waitFor(t, func() bool { return !r.Active() })
	if len(v.said()) < 3 {
		t.Fatalf("a long paragraph must be spoken in several parts: %d", len(v.said()))
	}
	for _, s := range v.said() {
		if len([]rune(s)) > 220 {
			t.Fatalf("part too long for ttsd: %d runes", len([]rune(s)))
		}
	}
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func TestReaderPausesBeforeMarkedPieces(t *testing.T) {
	v := &fakeVoice{}
	r := newTestReader(v)
	r.Pause = 60 * time.Millisecond
	r.Start(Reading{Title: "x", Pieces: []ReadPiece{{Para: 1, Text: "Antes."}, {Para: 1, Text: "Leamos el texto.", Pause: true}}}, 0)
	waitFor(t, func() bool { return len(v.said()) == 2 })
	v.mu.Lock()
	gap := v.startedAt[1].Sub(v.startedAt[0])
	v.mu.Unlock()
	if gap < 60*time.Millisecond {
		t.Fatalf("a marked piece must wait for the pause: %v", gap)
	}
	if _, ok := r.Jump(1); !ok {
		t.Fatal("jump must work")
	}
	waitFor(t, func() bool { return len(v.said()) == 4 })
	if v.said()[2] != Speakable("Antes.") {
		t.Fatalf("a paragraph jump starts at its first piece: %q", v.said()[2])
	}
}
