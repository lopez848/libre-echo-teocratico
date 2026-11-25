package main

import (
	"encoding/binary"
	"math"
	"testing"
)

func stereoTone(frames int, rate float64, freq float64, offset int) []byte {
	b := make([]byte, frames*4)
	for i := 0; i < frames; i++ {
		v := int16(10000 * math.Sin(2*math.Pi*freq*float64(i+offset)/rate))
		binary.LittleEndian.PutUint16(b[i*4:], uint16(v))
		binary.LittleEndian.PutUint16(b[i*4+2:], uint16(v))
	}
	return b
}

func TestResamplerRateAndContinuity(t *testing.T) {
	r := &resampler{from: 44100, to: 48000}
	src := stereoTone(44100, 44100, 440, 0) // 1 s continuous tone
	var out []byte
	for off, k := 0, 0; off < len(src)/4; k++ { // uneven chunk sizes
		n := 997 + 13*(k%5)
		if off+n > len(src)/4 {
			n = len(src)/4 - off
		}
		out = append(out, r.process(src[off*4:(off+n)*4])...)
		off += n
	}
	frames := len(out) / 4
	want := 48000.0
	if math.Abs(float64(frames)-want) > 3 {
		t.Fatalf("frames=%d want≈%.0f", frames, want)
	}
	// No clicks: consecutive output samples of a 440 Hz tone change smoothly.
	maxJump := 0.0
	for i := 1; i < frames; i++ {
		a := float64(int16(binary.LittleEndian.Uint16(out[(i-1)*4:])))
		b := float64(int16(binary.LittleEndian.Uint16(out[i*4:])))
		if d := math.Abs(b - a); d > maxJump {
			maxJump = d
		}
	}
	if limit := 2 * math.Pi * 440 / 48000 * 10000 * 1.5; maxJump > limit {
		t.Fatalf("discontinuity: jump %.0f > %.0f", maxJump, limit)
	}
}

func TestResamplerPassThroughAt48k(t *testing.T) {
	in := stereoTone(100, 48000, 440, 0)
	if got := (&resampler{from: 48000, to: 48000}).process(in); len(got) != len(in) {
		t.Fatal("48 kHz input must pass through")
	}
}
