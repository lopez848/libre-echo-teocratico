package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/tphakala/go-m4a/aacm4a"
)

// BusPlayer decodes MP3 itself and writes 48 kHz 16-bit stereo PCM to the
// audiod media bus. radiod only plays 48 kHz MP3 correctly; most jw.org
// songs are 44.1 kHz, so the bridge resamples them here.
type BusPlayer struct {
	Bus     string // /run/libreecho-audio/media.pcm
	HTTP    *http.Client
	Release func()   // frees the bus (stops radiod) before writing
	YT      *YouTube // resolves yt:<videoId> tracks to AAC audio

	mu      sync.Mutex
	cancel  context.CancelFunc
	playing bool
	paused  bool
	url     string
	gen     int
}

func (b *BusPlayer) Play(url, _, _ string) error {
	b.Stop()
	if b.Release != nil {
		b.Release()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.gen++
	gen := b.gen
	b.cancel, b.playing, b.paused, b.url = cancel, true, false, url
	b.mu.Unlock()
	go func() {
		err := b.stream(ctx, url)
		if err != nil && ctx.Err() == nil {
			logf("busplayer: %v", err)
		}
		b.mu.Lock()
		if b.gen == gen {
			b.playing = false
		}
		b.mu.Unlock()
	}()
	return nil
}

func (b *BusPlayer) Stop() error {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.playing, b.paused = false, false
	b.gen++
	b.mu.Unlock()
	return nil
}

func (b *BusPlayer) Pause() error  { b.setPaused(true); return nil }
func (b *BusPlayer) Resume() error { b.setPaused(false); return nil }

func (b *BusPlayer) setPaused(p bool) {
	b.mu.Lock()
	if b.playing {
		b.paused = p
	}
	b.mu.Unlock()
}

func (b *BusPlayer) isPaused() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.paused
}

func (b *BusPlayer) Status() (bool, bool, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.playing, b.paused, b.url, nil
}

// pcmSource is decoded s16le interleaved audio at Rate with Channels.
type pcmSource struct {
	R        io.Reader
	Rate     int
	Channels int
	Close    func()
}

// open decodes an MP3 URL or a yt:<videoId> track (AAC in m4a).
func (b *BusPlayer) open(ctx context.Context, url string) (*pcmSource, error) {
	if id, ok := strings.CutPrefix(url, "yt:"); ok {
		if b.YT == nil {
			return nil, fmt.Errorf("no YouTube client for %s", url)
		}
		u, err := b.YT.AudioURL(id)
		if err != nil {
			return nil, err
		}
		rr := &rangeReader{HTTP: b.HTTP, URL: u, Chunk: 1 << 20, Ctx: ctx}
		dec, info, err := aacm4a.NewDecoder(rr)
		if err != nil {
			return nil, fmt.Errorf("aac %s: %w", id, err)
		}
		return &pcmSource{R: dec, Rate: info.SampleRate, Channels: info.Channels, Close: func() {}}, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	dec, err := mp3.NewDecoder(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("mp3: %w", err)
	}
	return &pcmSource{R: dec, Rate: dec.SampleRate(), Channels: 2, Close: func() { resp.Body.Close() }}, nil
}

// toStereo duplicates mono s16le samples into both channels.
func toStereo(mono []byte) []byte {
	out := make([]byte, 0, len(mono)*2)
	for i := 0; i+1 < len(mono); i += 2 {
		out = append(out, mono[i], mono[i+1], mono[i], mono[i+1])
	}
	return out
}

func (b *BusPlayer) stream(ctx context.Context, url string) error {
	src, err := b.open(ctx, url)
	if err != nil {
		return err
	}
	defer src.Close()
	if src.Channels != 1 && src.Channels != 2 {
		return fmt.Errorf("unsupported channel count %d", src.Channels)
	}
	// O_NONBLOCK fails fast when nobody reads the bus; the file is then
	// driven by the runtime poller, so writes still wait for space.
	out, err := os.OpenFile(b.Bus, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open bus: %w", err)
	}
	defer out.Close()
	rs := &resampler{from: src.Rate, to: 48000}
	frame := 2 * src.Channels
	buf := make([]byte, 4096*frame)
	for {
		if ctx.Err() != nil {
			return nil
		}
		for b.isPaused() {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(200 * time.Millisecond):
			}
		}
		n, err := io.ReadFull(src.R, buf)
		if n -= n % frame; n > 0 {
			chunk := buf[:n]
			if src.Channels == 1 {
				chunk = toStereo(chunk)
			}
			if _, werr := out.Write(rs.process(chunk)); werr != nil {
				return fmt.Errorf("write bus: %w", werr)
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// resampler converts interleaved s16le stereo between rates by linear
// interpolation, carrying the last input frame across calls.
type resampler struct {
	from, to int
	pos      float64 // next output position, in frames, relative to prev
	prev     [2]int16
	havePrev bool
}

func (r *resampler) process(in []byte) []byte {
	if r.from == r.to || r.from <= 0 {
		return in
	}
	n := len(in) / 4
	if n == 0 {
		return nil
	}
	// x(-1) is the previous chunk's last frame; x(0..n-1) is this chunk.
	x := func(i int) (float64, float64) {
		if i < 0 {
			return float64(r.prev[0]), float64(r.prev[1])
		}
		return float64(int16(binary.LittleEndian.Uint16(in[i*4:]))), float64(int16(binary.LittleEndian.Uint16(in[i*4+2:])))
	}
	step := float64(r.from) / float64(r.to)
	t := r.pos - 1
	if !r.havePrev {
		t = 0
	}
	out := make([]byte, 0, int(float64(n)/step+2)*4)
	for {
		i := int(math.Floor(t))
		if i+1 > n-1 {
			break
		}
		f := t - float64(i)
		aL, aR := x(i)
		bL, bR := x(i + 1)
		out = binary.LittleEndian.AppendUint16(out, uint16(int16(aL+(bL-aL)*f)))
		out = binary.LittleEndian.AppendUint16(out, uint16(int16(aR+(bR-aR)*f)))
		t += step
	}
	l, rr := x(n - 1)
	r.prev = [2]int16{int16(l), int16(rr)}
	r.havePrev = true
	r.pos = t - float64(n-1)
	return out
}
