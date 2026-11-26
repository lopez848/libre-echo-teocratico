package main

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestScalePCM(t *testing.T) {
	in := make([]byte, 8)
	for i, v := range []int16{10000, -10000, 32767, -32768} {
		binary.LittleEndian.PutUint16(in[i*2:], uint16(v))
	}
	out := scalePCM(in, 0.15)
	want := []int16{1500, -1500, 4915, -4915}
	for i, w := range want {
		if got := int16(binary.LittleEndian.Uint16(out[i*2:])); got < w-1 || got > w+1 {
			t.Errorf("sample %d = %d, want %d", i, got, w)
		}
	}
	if got := scalePCM(in, 1); &got[0] != &in[0] {
		t.Error("full volume must not copy")
	}
}

func TestDuckWatchFollowsTheAssistant(t *testing.T) {
	var mu sync.Mutex
	listening := false
	var calls []bool
	d := &duckWatch{
		Poll: 2 * time.Millisecond, Hold: 20 * time.Millisecond,
		State: func() (bool, bool, error) { mu.Lock(); defer mu.Unlock(); return listening, false, nil },
		Duck:  func(on bool) { mu.Lock(); calls = append(calls, on); mu.Unlock() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	set := func(v bool) { mu.Lock(); listening = v; mu.Unlock() }
	got := func() []bool { mu.Lock(); defer mu.Unlock(); return append([]bool(nil), calls...) }
	time.Sleep(10 * time.Millisecond)
	if len(got()) != 0 {
		t.Fatalf("nothing to do while the assistant is idle: %v", got())
	}
	set(true)
	waitFor(t, func() bool { return len(got()) == 1 })
	if !got()[0] {
		t.Fatal("must duck when the assistant listens")
	}
	set(false)
	time.Sleep(8 * time.Millisecond)
	if len(got()) != 1 {
		t.Fatal("must hold the duck for a moment after it ends")
	}
	waitFor(t, func() bool { return len(got()) == 2 })
	if got()[1] {
		t.Fatal("must restore the volume after the hold")
	}
}

func TestBusPlayerDuckLowersTheOutput(t *testing.T) {
	b := &BusPlayer{}
	if b.gain() != 1 {
		t.Fatal("full volume by default")
	}
	b.SetDuck(true)
	if g := b.gain(); g < 0.1 || g > 0.2 {
		t.Fatalf("ducked gain %v", g)
	}
	b.SetDuck(false)
	if b.gain() != 1 {
		t.Fatal("restored")
	}
}

func TestAnnounceVoiceReadsTheMixerState(t *testing.T) {
	f := filepath.Join(t.TempDir(), "status.json")
	v := &announceVoice{Status: f}
	write := func(s string) { os.WriteFile(f, []byte(s), 0o644) }
	write(`{"state":"idle","active":null,"buses":{"media":true,"system":false,"announcement":false,"alarm":false}}`)
	if on, err := v.Speaking(); err != nil || on {
		t.Fatalf("music alone is not speech: %v %v", on, err)
	}
	write(`{"state":"announcing","active":"announcement","buses":{"media":false,"system":false,"announcement":true,"alarm":false}}`)
	if on, _ := v.Speaking(); !on {
		t.Fatal("an announcement is speech")
	}
	os.Remove(f)
	if _, err := v.Speaking(); err == nil {
		t.Fatal("a missing status file is an error")
	}
}

// pauseRadioState tracks a paused flag like the bus player does.
type pauseRadioState struct {
	fakeRadio
	paused bool
}

func (p *pauseRadioState) Pause() error  { p.mu.Lock(); p.paused = true; p.mu.Unlock(); return nil }
func (p *pauseRadioState) Resume() error { p.mu.Lock(); p.paused = false; p.mu.Unlock(); return nil }
func (p *pauseRadioState) Status() (bool, bool, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing, p.paused, "", nil
}

func TestHoldForVoicePausesAndResumes(t *testing.T) {
	old := *svc
	defer func() { *svc = old; pausedPlayer = false }()
	r := &pauseRadioState{}
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	svc.Player.Start("x", []Track{{URL: "u1"}}, false)
	h := &voiceHold{}

	h.Set(true) // the assistant starts listening
	if _, paused, _, _ := r.Status(); !paused {
		t.Fatal("must pause while the assistant listens")
	}
	h.Set(false) // a quick question: go on
	if _, paused, _, _ := r.Status(); paused {
		t.Fatal("must resume after the turn")
	}

	h.Set(true)
	pausedPlayer = true // the command was "pausa"
	h.Set(false)
	if _, paused, _, _ := r.Status(); !paused {
		t.Fatal("after \"pausa\" it must stay paused")
	}
	pausedPlayer = false
	r.Resume()

	h.Set(true)
	svc.Player.Stop() // the command was "para"
	h.Set(false)
	if r.count() != 1 || svc.Player.Active() {
		t.Fatal("after \"para\" nothing plays again")
	}

	r2 := &pauseRadioState{}
	svc.Player = &Player{Radio: r2, Poll: time.Hour}
	h.Set(true) // nothing playing: nothing to pause or resume
	h.Set(false)
	if _, paused, _, _ := r2.Status(); paused {
		t.Fatal("idle player must be left alone")
	}
}

// When the assistant starts listening (button or wake word), the bridge's
// own speech must stop, or agentd drops the command as "a voice response is
// already playing".
func TestDuckWatchCallsOnListenOnce(t *testing.T) {
	var mu sync.Mutex
	rec := false
	listens := 0
	d := &duckWatch{
		Poll: 2 * time.Millisecond, Hold: 5 * time.Millisecond,
		State:    func() (bool, bool, error) { mu.Lock(); defer mu.Unlock(); return rec, false, nil },
		Duck:     func(bool) {},
		OnListen: func() { mu.Lock(); listens++; mu.Unlock() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	get := func() int { mu.Lock(); defer mu.Unlock(); return listens }
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	rec = true
	mu.Unlock()
	waitFor(t, func() bool { return get() == 1 })
	time.Sleep(15 * time.Millisecond)
	if get() != 1 {
		t.Fatal("once per listening turn")
	}
	mu.Lock()
	rec = false
	mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	rec = true
	mu.Unlock()
	waitFor(t, func() bool { return get() == 2 })
}

// agentd speaks a reply as a queue of sentences and starts the next one as
// soon as the current one is stopped (2026-11-26 logs: "stop speech
// requested" then "speak requested (64 chars)" in the same second), so the
// bridge must keep cutting while the assistant listens until the queue is
// empty.
func TestDuckWatchCallsWhileListeningOnEveryPoll(t *testing.T) {
	var mu sync.Mutex
	rec := false
	ticks := 0
	d := &duckWatch{
		Poll: 2 * time.Millisecond, Hold: 5 * time.Millisecond,
		State:          func() (bool, bool, error) { mu.Lock(); defer mu.Unlock(); return rec, false, nil },
		Duck:           func(bool) {},
		WhileListening: func() { mu.Lock(); ticks++; mu.Unlock() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	get := func() int { mu.Lock(); defer mu.Unlock(); return ticks }
	time.Sleep(10 * time.Millisecond)
	if get() != 0 {
		t.Fatal("not called while the assistant is idle")
	}
	mu.Lock()
	rec = true
	mu.Unlock()
	waitFor(t, func() bool { return get() >= 3 })
	mu.Lock()
	rec = false
	mu.Unlock()
	time.Sleep(6 * time.Millisecond)
	n := get()
	time.Sleep(10 * time.Millisecond)
	if get() != n {
		t.Fatal("must stop once the assistant stops listening")
	}
}

func TestCutQueuedSpeechStopsEachSentenceThatStarts(t *testing.T) {
	queue := 3 // sentences agentd still has to say
	speaking := true
	stops := 0
	c := &speechCutter{
		Speaking: func() (bool, error) { return speaking, nil },
		Stop: func() {
			stops++
			queue--
			speaking = queue > 0 // agentd starts the next sentence at once
		},
	}
	for i := 0; i < 10; i++ {
		c.Tick()
	}
	if stops != 3 || speaking {
		t.Fatalf("stops=%d speaking=%v, want every queued sentence cut", stops, speaking)
	}
	if !c.Cut() {
		t.Fatal("Cut must report that it stopped something")
	}
	c.Reset()
	if c.Cut() {
		t.Fatal("Reset clears the report for the next listening turn")
	}
}
