package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Remote takes typed commands from a private ntfy topic, runs each one
// like a voice turn and posts Mia's answer back to the same topic. The
// topic name is the only secret: anyone who knows it can command the
// speaker, so it must be long and random and live only on the device and
// the owner's phone.
type Remote struct {
	Server, Topic string
	HTTP          *http.Client // no overall timeout: the stream stays open
	Answer        func(ctx context.Context, text string) string
	Speak         func(text string) // says the answer aloud on the Echo; nil skips it
	Retry         time.Duration     // wait before reconnecting
	last          string            // id of the last message seen
}

const (
	remoteMaxLen   = 500
	remoteReplyTag = "mia" // marks Mia's own replies so they are not run
	minTopicLen    = 24
)

type ntfyEvent struct {
	ID      string   `json:"id"`
	Event   string   `json:"event"`
	Message string   `json:"message"`
	Tags    []string `json:"tags"`
}

// Run subscribes until ctx ends, reconnecting after errors. On reconnect
// it asks for the messages after the last one seen, so none is lost or run
// twice.
func (r *Remote) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.stream(ctx); err != nil && ctx.Err() == nil {
			log.Printf("remote: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(r.Retry):
		}
	}
}

func (r *Remote) stream(ctx context.Context) error {
	u := strings.TrimRight(r.Server, "/") + "/" + url.PathEscape(r.Topic) + "/json"
	if r.last != "" {
		u += "?since=" + url.QueryEscape(r.last)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("subscribe answered %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		var ev ntfyEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Event != "message" {
			continue
		}
		r.last = ev.ID
		if hasTag(ev.Tags, remoteReplyTag) {
			continue
		}
		text := strings.TrimSpace(ev.Message)
		if text == "" {
			continue
		}
		var reply string
		if len([]rune(text)) > remoteMaxLen {
			reply = "Ese mensaje es muy largo. Escríbeme la orden en pocas palabras."
		} else {
			logf("remote command %q", text)
			reply = r.Answer(ctx, text)
			if r.Speak != nil {
				r.Speak(reply)
			}
		}
		if err := r.post(ctx, reply); err != nil {
			log.Printf("remote reply: %v", err)
		}
	}
	return sc.Err()
}

func (r *Remote) post(ctx context.Context, text string) error {
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(r.Server, "/")+"/"+url.PathEscape(r.Topic), strings.NewReader(text))
	if err != nil {
		return err
	}
	req.Header.Set("Title", "Mia")
	req.Header.Set("Tags", remoteReplyTag)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy answered %s", resp.Status)
	}
	return nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// loadRemote reads "topic=" and optional "server=" lines. A missing file
// disables remote commands; a short topic or a file others can read is
// refused because the topic is the password.
func loadRemote(path string) (*Remote, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must be readable only by its owner (chmod 600)", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r := &Remote{Server: "https://ntfy.sh", Retry: 10 * time.Second}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		switch strings.TrimSpace(k) {
		case "topic":
			r.Topic = strings.TrimSpace(v)
		case "server":
			r.Server = strings.TrimSpace(v)
		}
	}
	if len(r.Topic) < minTopicLen {
		return nil, fmt.Errorf("%s: topic must have at least %d random characters", path, minTopicLen)
	}
	return r, nil
}
