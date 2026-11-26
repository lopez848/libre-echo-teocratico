package main

import (
	"context"
	"encoding/binary"
	"math"
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

// duckWatch polls whether agentd is busy with a voice turn and ducks the
// music while it is, holding the duck for a moment after.
type duckWatch struct {
	Status func() (busy bool, err error)
	Duck   func(on bool)
	Poll   time.Duration
	Hold   time.Duration
}

func (d *duckWatch) Run(ctx context.Context) {
	ducked := false
	var lastBusy time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.Poll):
		}
		busy, err := d.Status()
		if err != nil {
			continue
		}
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

// agentBusy asks agentd whether it is recognizing or answering a turn.
func agentBusy(sock string) (bool, error) {
	resp, err := (&LocalTTS{Socket: sock}).call(map[string]any{"v": 1, "id": 7, "cmd": "status", "args": map[string]any{}})
	if err != nil {
		return false, err
	}
	data, _ := resp["data"].(map[string]any)
	rec, _ := data["recognizing"].(bool)
	disp, _ := data["dispatching"].(bool)
	return rec || disp, nil
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
