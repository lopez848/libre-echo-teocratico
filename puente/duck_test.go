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
		Status: func() (bool, error) { mu.Lock(); defer mu.Unlock(); return listening, nil },
		Duck:   func(on bool) { mu.Lock(); calls = append(calls, on); mu.Unlock() },
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
