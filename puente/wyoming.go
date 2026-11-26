package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/text/unicode/norm"
)

// Synth produces 16-bit mono PCM for a sentence.
type Synth interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
}

// Fallback speaks a sentence by other means (the local Piper ttsd) and
// returns once it has finished.
type Fallback interface {
	Speak(text string) error
}

// WyomingServer implements the TTS side of the Wyoming protocol for
// libreecho-ttsd-wyoming: describe -> info, synthesize -> audio-*.
type WyomingServer struct {
	Synth    Synth
	Fallback Fallback
	Voice    string
	Rate     int
	Timeout  time.Duration
}

type wyEvent struct {
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data,omitempty"`
	DataLength    int             `json:"data_length,omitempty"`
	PayloadLength int             `json:"payload_length,omitempty"`
}

func nfc(s string) string { return norm.NFC.String(s) }

func readEvent(r *bufio.Reader) (wyEvent, []byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return wyEvent{}, nil, err
	}
	var ev wyEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return wyEvent{}, nil, fmt.Errorf("bad header: %w", err)
	}
	if ev.DataLength < 0 || ev.DataLength > 1<<20 || ev.PayloadLength < 0 || ev.PayloadLength > 16<<20 {
		return wyEvent{}, nil, errors.New("event too large")
	}
	if ev.DataLength > 0 {
		d := make([]byte, ev.DataLength)
		if _, err := io.ReadFull(r, d); err != nil {
			return wyEvent{}, nil, err
		}
		ev.Data = d
	}
	var payload []byte
	if ev.PayloadLength > 0 {
		payload = make([]byte, ev.PayloadLength)
		if _, err := io.ReadFull(r, payload); err != nil {
			return wyEvent{}, nil, err
		}
	}
	return ev, payload, nil
}

func writeEvent(w io.Writer, typ string, data any, payload []byte) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	h := map[string]any{"type": typ, "data_length": len(d)}
	if len(payload) > 0 {
		h["payload_length"] = len(payload)
	}
	hb, _ := json.Marshal(h)
	buf := append(append(hb, '\n'), d...)
	buf = append(buf, payload...)
	_, err = w.Write(buf)
	return err
}

func (s *WyomingServer) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(c)
	}
}

func (s *WyomingServer) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		c.SetDeadline(time.Now().Add(2 * time.Minute))
		ev, _, err := readEvent(r)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logf("wyoming: %v", err)
			}
			return
		}
		switch ev.Type {
		case "describe":
			if err := writeEvent(c, "info", s.info(), nil); err != nil {
				return
			}
		case "synthesize":
			var d struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			if err := s.synthesize(c, d.Text); err != nil {
				logf("wyoming: %v", err)
				return
			}
		}
	}
}

func (s *WyomingServer) info() any {
	attr := map[string]string{"name": "NVIDIA", "url": "https://build.nvidia.com"}
	return map[string]any{"tts": []any{map[string]any{
		"name": "puente-riva", "description": "NVIDIA Magpie TTS via puente", "attribution": attr,
		"installed": true, "version": "1",
		"voices": []any{map[string]any{"name": s.Voice, "description": s.Voice, "attribution": attr,
			"installed": true, "languages": []string{"es"}, "version": "1"}},
	}}}
}

// synthesize speaks text through the cloud voice, or through the fallback
// when the cloud fails; the client always gets a complete audio stream.
func (s *WyomingServer) synthesize(w io.Writer, text string) error {
	text = cleanForCloud(text)
	format := map[string]int{"rate": s.Rate, "width": 2, "channels": 1}
	if err := writeEvent(w, "audio-start", format, nil); err != nil {
		return err
	}
	// Long texts go out in sentence groups: the first audio starts sooner
	// and each cloud request stays small.
	parts := SplitSentences(text, 300)
	for n, part := range parts {
		ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
		pcm, err := s.Synth.Synthesize(ctx, part)
		cancel()
		if err != nil {
			logf("cloud tts failed, using local voice: %v", err)
			if s.Fallback != nil {
				rest := strings.Join(parts[n:], " ")
				if ferr := s.Fallback.Speak(Speakable(rest)); ferr != nil {
					logf("local voice failed too: %v", ferr)
				}
			}
			break
		}
		const chunk = 8192
		for i := 0; i < len(pcm); i += chunk {
			j := i + chunk
			if j > len(pcm) {
				j = len(pcm)
			}
			if err := writeEvent(w, "audio-chunk", format, pcm[i:j]); err != nil {
				return err
			}
		}
	}
	return writeEvent(w, "audio-stop", map[string]any{}, nil)
}

var localReqID int64

// LocalTTS drives a second libreecho-ttsd (local Piper) over its socket.
type LocalTTS struct {
	Socket string
}

func (l *LocalTTS) call(req map[string]any) (map[string]any, error) {
	c, err := net.DialTimeout("unix", l.Socket, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		return nil, err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		return resp, fmt.Errorf("local ttsd: %v", resp["error"])
	}
	return resp, nil
}

// Speaking reports whether this ttsd is speaking now.
func (l *LocalTTS) Speaking() (bool, error) {
	id := atomic.AddInt64(&localReqID, 2) & 0x3fffffff
	resp, err := l.call(map[string]any{"v": 1, "id": id, "cmd": "status"})
	if err != nil {
		return false, err
	}
	data, _ := resp["data"].(map[string]any)
	speaking, _ := data["speaking"].(bool)
	return speaking, nil
}

func (l *LocalTTS) Speak(text string) error {
	// ttsd parses ids as a 32-bit long: keep them small and positive.
	id := atomic.AddInt64(&localReqID, 2) & 0x3fffffff
	if _, err := l.call(map[string]any{"v": 1, "id": id, "cmd": "speak", "text": text}); err != nil {
		return err
	}
	// Wait until it finishes so the caller's turn ends after the speech.
	deadline := time.Now().Add(90 * time.Second)
	time.Sleep(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		resp, err := l.call(map[string]any{"v": 1, "id": id + 1, "cmd": "status"})
		if err != nil {
			return err
		}
		data, _ := resp["data"].(map[string]any)
		if speaking, _ := data["speaking"].(bool); !speaking {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("local ttsd: timeout")
}

// SplitSentences groups sentences into chunks of at most max runes
// (a single longer sentence is split at commas or spaces).
func SplitSentences(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var sentences []string
	start := 0
	r := []rune(text)
	for i, c := range r {
		if (c == '.' || c == '?' || c == '!' || c == ';') && (i+1 == len(r) || r[i+1] == ' ') {
			sentences = append(sentences, strings.TrimSpace(string(r[start:i+1])))
			start = i + 1
		}
	}
	if start < len(r) {
		if tail := strings.TrimSpace(string(r[start:])); tail != "" {
			sentences = append(sentences, tail)
		}
	}
	var out []string
	cur := ""
	flush := func() {
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	for _, s := range sentences {
		for len([]rune(s)) > max {
			cut := strings.LastIndex(string([]rune(s)[:max]), ", ")
			if cut <= 0 {
				cut = strings.LastIndex(string([]rune(s)[:max]), " ")
			}
			if cut <= 0 {
				cut = len(string([]rune(s)[:max]))
			}
			flush()
			out = append(out, strings.TrimSpace(s[:cut+1]))
			s = strings.TrimSpace(s[cut+1:])
		}
		if cur != "" && len([]rune(cur))+1+len([]rune(s)) > max {
			flush()
		}
		if cur == "" {
			cur = s
		} else {
			cur += " " + s
		}
	}
	flush()
	return out
}
