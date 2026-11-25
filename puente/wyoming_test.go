package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeSynth struct {
	pcm  []byte
	err  error
	text string
}

func (f *fakeSynth) Synthesize(_ context.Context, text string) ([]byte, error) {
	f.text = text
	return f.pcm, f.err
}

type fakeFallback struct{ said []string }

func (f *fakeFallback) Speak(t string) error { f.said = append(f.said, t); return nil }

func roundTrip(t *testing.T, s *WyomingServer, text string) []wyEvent {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.Serve(l)
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	d, _ := json.Marshal(map[string]any{"text": text, "voice": map[string]string{"name": "x"}})
	c.Write([]byte(`{"type":"synthesize","data_length":` + itoa(len(d)) + "}\n"))
	c.Write(d)
	r := bufio.NewReader(c)
	var evs []wyEvent
	for {
		ev, payload, err := readEvent(r)
		if err != nil {
			t.Fatalf("read: %v (got %d events)", err, len(evs))
		}
		ev.PayloadLength = len(payload)
		evs = append(evs, ev)
		if ev.Type == "audio-stop" {
			return evs
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestWyomingStreamsCloudAudio(t *testing.T) {
	fs := &fakeSynth{pcm: bytes.Repeat([]byte{1, 0}, 10000)}
	fb := &fakeFallback{}
	evs := roundTrip(t, &WyomingServer{Synth: fs, Fallback: fb, Voice: "v", Rate: 22050, Timeout: time.Second}, "¿En qué te ayudo?")
	if evs[0].Type != "audio-start" || !strings.Contains(string(evs[0].Data), `"rate":22050`) {
		t.Fatalf("first event %+v", evs[0])
	}
	total := 0
	for _, e := range evs {
		if e.Type == "audio-chunk" {
			total += e.PayloadLength
		}
	}
	if total != 20000 || len(fb.said) != 0 {
		t.Fatalf("audio bytes=%d fallback=%v", total, fb.said)
	}
	if fs.text != "¿En qué te ayudo?" {
		t.Fatalf("cloud must get NFC text, got %q", fs.text)
	}
}

func TestWyomingFallsBackToLocalVoice(t *testing.T) {
	fb := &fakeFallback{}
	evs := roundTrip(t, &WyomingServer{Synth: &fakeSynth{err: errors.New("offline")}, Fallback: fb, Voice: "v", Rate: 22050, Timeout: time.Second}, "¿Qué hora es?")
	if len(evs) != 2 || evs[0].Type != "audio-start" || evs[1].Type != "audio-stop" {
		t.Fatalf("events %+v", evs)
	}
	if len(fb.said) != 1 || fb.said[0] != Speakable("¿Qué hora es?") {
		t.Fatalf("fallback got %q", fb.said)
	}
}

func TestRivaProtobufRoundTrip(t *testing.T) {
	req := synthRequest("hola", "es-US", "V", 22050)
	if !bytes.Contains(req, []byte("hola")) || req[0] != 0x0a {
		t.Fatalf("bad request %x", req)
	}
	resp := append(pbString(1, "\x01\x02\x03"), pbInt(3, 7)...)
	got, err := audioField(resp)
	if err != nil || string(got) != "\x01\x02\x03" {
		t.Fatalf("audioField=%x %v", got, err)
	}
}

func TestLocalTTSUsesSmallRequestIDs(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/t.sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var ids []float64
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			var req map[string]any
			json.NewDecoder(c).Decode(&req)
			ids = append(ids, req["id"].(float64))
			if req["cmd"] == "status" {
				c.Write([]byte(`{"v":1,"ok":true,"data":{"speaking":false}}` + "\n"))
			} else {
				c.Write([]byte(`{"v":1,"ok":true,"data":{"speaking":true}}` + "\n"))
			}
			c.Close()
		}
	}()
	if err := (&LocalTTS{Socket: sock}).Speak("hola"); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id <= 0 || id >= 1<<31 {
			t.Fatalf("id %v out of 32-bit range", id)
		}
	}
}
