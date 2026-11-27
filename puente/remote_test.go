package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNtfy streams the queued lines on GET /<topic>/json and records POSTs.
type fakeNtfy struct {
	mu      sync.Mutex
	lines   []string
	since   []string
	posts   []string
	headers []http.Header
}

func (f *fakeNtfy) handler(topic string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/"+topic+"/json":
			f.mu.Lock()
			f.since = append(f.since, r.URL.Query().Get("since"))
			lines := f.lines
			f.lines = nil
			f.mu.Unlock()
			for _, l := range lines {
				fmt.Fprintln(w, l)
			}
			w.(http.Flusher).Flush()
			select { // keep the stream open briefly like ntfy does
			case <-r.Context().Done():
			case <-time.After(30 * time.Millisecond):
			}
		case r.Method == "POST" && r.URL.Path == "/"+topic:
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.posts = append(f.posts, string(b))
			f.headers = append(f.headers, r.Header.Clone())
			f.mu.Unlock()
		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fakeNtfy) snapshot() ([]string, []string, []http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...), append([]string(nil), f.since...), append([]http.Header(nil), f.headers...)
}

func TestRemoteRunsCommandsAndRepliesOnTheTopic(t *testing.T) {
	topic := "mia-0123456789abcdefghijklmn"
	f := &fakeNtfy{lines: []string{
		`{"id":"a1","event":"open"}`,
		`{"id":"m1","event":"message","message":"apaga la luz"}`,
		`{"id":"m2","event":"message","message":"Listo.","tags":["mia"]}`, // our own reply
		`{"id":"m3","event":"keepalive"}`,
		`{"id":"m4","event":"message","message":"` + strings.Repeat("x", 600) + `"}`,
	}}
	srv := httptest.NewServer(f.handler(topic))
	defer srv.Close()
	var mu sync.Mutex
	var got []string
	r := &Remote{Server: srv.URL, Topic: topic, HTTP: srv.Client(), Retry: 5 * time.Millisecond,
		Answer: func(_ context.Context, text string) string {
			mu.Lock()
			got = append(got, text)
			mu.Unlock()
			return "Apagué la luz."
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	waitFor(t, func() bool { posts, since, _ := f.snapshot(); return len(posts) == 2 && len(since) >= 2 })
	cancel()
	<-done
	posts, since, headers := f.snapshot()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "apaga la luz" {
		t.Fatalf("commands run: %q (own replies and over-long messages are skipped)", got)
	}
	if posts[0] != "Apagué la luz." || headers[0].Get("Tags") != "mia" || headers[0].Get("Title") != "Mia" {
		t.Fatalf("reply %q headers %v", posts[0], headers[0])
	}
	if !strings.Contains(posts[1], "muy largo") {
		t.Fatalf("an over-long message gets a short refusal: %q", posts[1])
	}
	if since[0] != "" || since[1] != "m4" {
		t.Fatalf("reconnect must continue after the last message seen: %q", since)
	}
}

func TestLoadRemoteConfig(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "remote.conf")
	if r, err := loadRemote(f); r != nil || err != nil {
		t.Fatalf("a missing file disables remote commands: %v %v", r, err)
	}
	os.WriteFile(f, []byte("topic=short\n"), 0o600)
	if _, err := loadRemote(f); err == nil {
		t.Fatal("a short, guessable topic must be refused")
	}
	os.WriteFile(f, []byte("topic=mia-0123456789abcdefghijklmn\n"), 0o644)
	os.Chmod(f, 0o644)
	if _, err := loadRemote(f); err == nil {
		t.Fatal("a file others can read must be refused")
	}
	os.WriteFile(f, []byte("# comment\ntopic=mia-0123456789abcdefghijklmn\n"), 0o600)
	os.Chmod(f, 0o600)
	r, err := loadRemote(f)
	if err != nil || r.Topic != "mia-0123456789abcdefghijklmn" || r.Server != "https://ntfy.sh" {
		t.Fatalf("config %+v %v", r, err)
	}
}
