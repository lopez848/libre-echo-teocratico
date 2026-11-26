package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"sync"
	"time"
)

// Music played by the bridge goes straight to the media bus, so agentd
// cannot pause it like the stock radio while it listens to a command: the
// command then competes with the music. The bridge lowers its own output
// while agentd is recognizing or answering, as speakers do.

const duckGain = 0.15

// scalePCM scales s16le samples by gain (a copy unless gain is 1).
func scalePCM(in []byte, gain float64) []byte {
	if gain >= 1 {
		return in
	}
	out := make([]byte, len(in))
	for i := 0; i+1 < len(in); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(in[i:]))) * gain
		binary.LittleEndian.PutUint16(out[i:], uint16(int16(math.Round(v))))
	}
	return out
}

// duckWatch polls agentd's voice turn state. While it listens or answers,
// Duck(true) holds the bridge's playback (released a moment after); when it
// starts listening, OnListen stops the bridge's own speech so the command
// is not dropped as "a voice response is already playing".
type duckWatch struct {
	State    func() (recognizing, dispatching bool, err error)
	Duck     func(on bool)
	OnListen func()
	// WhileListening runs on every poll while agentd is recognizing.
	WhileListening func()
	Poll           time.Duration
	Hold           time.Duration
}

func (d *duckWatch) Run(ctx context.Context) {
	ducked, wasListening := false, false
	var lastBusy time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.Poll):
		}
		rec, disp, err := d.State()
		if err != nil {
			continue
		}
		if rec && !wasListening && d.OnListen != nil {
			d.OnListen()
		}
		if rec && d.WhileListening != nil {
			d.WhileListening()
		}
		wasListening = rec
		busy := rec || disp
		switch {
		case busy:
			lastBusy = time.Now()
			if !ducked {
				d.Duck(true)
				ducked = true
			}
		case ducked && time.Since(lastBusy) >= d.Hold:
			d.Duck(false)
			ducked = false
		}
	}
}

// speechCutter empties agentd's reply queue while the assistant listens.
// agentd speaks a reply one sentence at a time and starts the next as soon
// as the current one is stopped; while any reply plays it drops new voice
// commands ("a voice response is already playing"). Stopping every sentence
// that starts during the user's command drains the queue before the
// transcript is ready.
type speechCutter struct {
	Speaking func() (bool, error)
	Stop     func()
	cut      bool
}

// Tick stops the announcement being spoken, if any.
func (c *speechCutter) Tick() {
	if on, err := c.Speaking(); err == nil && on {
		c.Stop()
		c.cut = true
	}
}

// Cut reports whether Tick stopped something since the last Reset.
func (c *speechCutter) Cut() bool { return c.cut }

func (c *speechCutter) Reset() { c.cut = false }

// agentState asks agentd whether it is recognizing or answering a turn.
func agentState(sock string) (bool, bool, error) {
	resp, err := (&LocalTTS{Socket: sock}).call(map[string]any{"v": 1, "id": 7, "cmd": "status", "args": map[string]any{}})
	if err != nil {
		return false, false, err
	}
	data, _ := resp["data"].(map[string]any)
	rec, _ := data["recognizing"].(bool)
	disp, _ := data["dispatching"].(bool)
	return rec, disp, nil
}

func (b *BusPlayer) SetDuck(on bool) {
	if on {
		b.duck.Store(1)
	} else {
		b.duck.Store(0)
	}
}

func (b *BusPlayer) gain() float64 {
	if b.duck.Load() == 1 {
		return duckGain
	}
	return 1
}

// announceVoice speaks through the main ttsd but tells whether anything is
// being spoken from audiod's mixer state: agentd's replies run in ttsd child
// processes, so the daemon's own "speaking" flag stays false while they
// play. Between two sentences of a reply the mixer goes idle for about a
// second, so callers wait for a longer silence.
type announceVoice struct {
	TTS    *LocalTTS
	Status string // /run/libreecho-audio/status.json
}

func (a *announceVoice) Speak(text string) error { return a.TTS.Speak(text) }

func (a *announceVoice) Speaking() (bool, error) {
	b, err := os.ReadFile(a.Status)
	if err != nil {
		return false, err
	}
	var st struct {
		State string `json:"state"`
		Buses struct {
			Announcement bool `json:"announcement"`
		} `json:"buses"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return false, err
	}
	return st.Buses.Announcement || st.State == "announcing", nil
}

// voiceHold pauses what the bridge is playing while agentd listens to and
// answers a voice command (wake word or action button), and resumes it
// after, unless the command paused it ("pausa"), stopped it ("para") or
// started something else.
type voiceHold struct {
	mu    sync.Mutex
	held  bool
	radio Radio
}

func (h *voiceHold) Set(busy bool) {
	if svc.Player == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if busy {
		if h.held || !svc.Player.Active() {
			return
		}
		playing, paused, _, err := svc.Player.Radio.Status()
		if err != nil || !playing || paused {
			return
		}
		if svc.Player.Radio.Pause() == nil {
			h.held, h.radio = true, svc.Player.Radio
		}
		return
	}
	if !h.held {
		return
	}
	h.held = false
	if pausedPlayer || !svc.Player.Active() || svc.Player.Radio != h.radio {
		return
	}
	if _, paused, _, err := svc.Player.Radio.Status(); err == nil && paused {
		_ = svc.Player.Radio.Resume()
	}
}
