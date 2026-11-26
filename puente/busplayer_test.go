package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphakala/go-aac/pcm"
	"github.com/tphakala/go-m4a/aacm4a"
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

// m4aTone encodes one second of a 440 Hz tone as AAC-LC in an m4a file.
func m4aTone(t *testing.T, channels int) []byte {
	t.Helper()
	const rate = 44100
	raw := make([]byte, 0, rate*2*channels)
	for i := 0; i < rate; i++ {
		v := int16(10000 * math.Sin(2*math.Pi*440*float64(i)/rate))
		for c := 0; c < channels; c++ {
			raw = binary.LittleEndian.AppendUint16(raw, uint16(v))
		}
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "tone.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := aacm4a.EncodeInterleaved(f, pcm.Config{SampleRate: rate, BitDepth: 16, Channels: channels, Bitrate: 128000}, raw); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.Name())
	return b
}

// fakeYouTubeAudio serves a player answer pointing at an m4a file that
// only answers ranged requests, like googlevideo.
func fakeYouTubeAudio(t *testing.T, file []byte) *YouTube {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio.m4a" {
			if r.Header.Get("Range") == "" {
				http.Error(w, "plain GET", http.StatusForbidden)
				return
			}
			http.ServeContent(w, r, "a.m4a", time.Time{}, bytes.NewReader(file))
			return
		}
		fmt.Fprintf(w, `{"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[{"itag":140,"mimeType":"audio/mp4","url":"%s/audio.m4a"}]}}`, srv.URL)
	}))
	t.Cleanup(srv.Close)
	return &YouTube{HTTP: srv.Client(), Web: srv.URL}
}

// pcmStats returns frames and peak amplitude of s16 stereo PCM.
func pcmStats(b []byte) (frames int, peak int) {
	for i := 0; i+1 < len(b); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(b[i:])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return len(b) / 4, peak
}

func TestBusPlayerStreamsYouTubeAAC(t *testing.T) {
	for _, ch := range []int{2, 1} {
		t.Run(fmt.Sprint(ch, "ch"), func(t *testing.T) {
			bus := filepath.Join(t.TempDir(), "media.pcm")
			os.WriteFile(bus, nil, 0o644)
			b := &BusPlayer{Bus: bus, HTTP: http.DefaultClient, YT: fakeYouTubeAudio(t, m4aTone(t, ch))}
			if err := b.stream(context.Background(), "yt:abc"); err != nil {
				t.Fatal(err)
			}
			out, _ := os.ReadFile(bus)
			frames, peak := pcmStats(out)
			// One second at 48 kHz stereo, give or take the AAC priming frames.
			if frames < 47000 || frames > 51000 {
				t.Fatalf("frames=%d, want about 48000", frames)
			}
			if peak < 5000 || peak > 12000 {
				t.Fatalf("peak=%d, want the tone (~10000)", peak)
			}
		})
	}
}

func TestBusPlayerYouTubeErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"playabilityStatus":{"status":"UNPLAYABLE","reason":"blocked"}}`)
	}))
	defer srv.Close()
	bus := filepath.Join(t.TempDir(), "media.pcm")
	os.WriteFile(bus, nil, 0o644)
	b := &BusPlayer{Bus: bus, HTTP: http.DefaultClient, YT: &YouTube{HTTP: srv.Client(), Web: srv.URL}}
	if err := b.stream(context.Background(), "yt:abc"); err == nil {
		t.Fatal("an unplayable video must return an error")
	}
	b.YT = nil
	if err := b.stream(context.Background(), "yt:abc"); err == nil {
		t.Fatal("yt: without a YouTube client must return an error")
	}
}
