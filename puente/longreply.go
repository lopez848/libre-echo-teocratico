package main

import (
	"context"
	"sync"
	"time"
)

// agentdReplyMax is how many bytes of a reply the bridge hands to agentd.
// agentd keeps only about the first 635 bytes of a streamed reply and drops
// the rest (2026-11-26: the daily text stopped mid-way), so longer replies
// send their first sentences to agentd and the bridge speaks the rest.
const agentdReplyMax = 560

// splitForAgentd returns the whole sentences that fit agentd's limit once
// made speakable, and the remaining pieces for the bridge to speak.
func splitForAgentd(text string, max int) (string, []string) {
	if len(Speakable(text)) <= max {
		return text, nil
	}
	parts := SplitSentences(text, 200)
	head := ""
	for i, p := range parts {
		next := p
		if head != "" {
			next = head + " " + p
		}
		if len(Speakable(next)) > max {
			if head == "" { // a single huge piece: let agentd take what fits
				return p, parts[i+1:]
			}
			return head, parts[i:]
		}
		head = next
	}
	return head, nil
}

// voice is a ttsd instance the bridge can speak through.
type voice interface {
	Speak(text string) error // returns when the text has been spoken
	Speaking() (bool, error)
}

// tailSpeaker speaks the rest of a long reply after agentd finished the
// first part. A new turn cancels it.
type tailSpeaker struct {
	Voice     voice
	Poll      time.Duration // status polling interval
	Idle      time.Duration // silence that means agentd is done
	StartWait time.Duration // how long to wait for agentd to start speaking

	mu     sync.Mutex
	cancel context.CancelFunc
	gen    int
}

// Speaking reports whether a long reply tail is pending or being spoken.
func (t *tailSpeaker) Speaking() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancel != nil
}

func (t *tailSpeaker) Cancel() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.mu.Unlock()
}

func (t *tailSpeaker) Start(pieces []string) {
	if t == nil || len(pieces) == 0 {
		return
	}
	t.Cancel()
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	t.cancel = cancel
	t.gen++
	gen := t.gen
	t.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			t.mu.Lock()
			if t.gen == gen {
				t.cancel = nil // done: nothing pending any more
			}
			t.mu.Unlock()
		}()
		if !t.waitSilence(ctx) {
			return
		}
		for _, p := range pieces {
			if ctx.Err() != nil {
				return
			}
			if err := t.Voice.Speak(Speakable(p)); err != nil {
				logf("long reply voice failed: %v", err)
				return
			}
		}
	}()
}

// waitSilence waits for agentd to start speaking the first part (or
// StartWait to pass), then for Idle of continuous silence.
func (t *tailSpeaker) waitSilence(ctx context.Context) bool {
	return waitSilence(ctx, t.Voice, t.Poll, t.Idle, t.StartWait)
}

func waitSilence(ctx context.Context, v voice, poll, idle, startWait time.Duration) bool {
	sleep := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
			return true
		}
	}
	deadline := time.Now().Add(startWait)
	for time.Now().Before(deadline) {
		if on, err := v.Speaking(); err == nil && on {
			break
		}
		if !sleep() {
			return false
		}
	}
	quietSince := time.Time{}
	for {
		on, err := v.Speaking()
		if err != nil {
			logf("voice status: %v", err)
			return false
		}
		if on {
			quietSince = time.Time{}
		} else if quietSince.IsZero() {
			quietSince = time.Now()
		} else if time.Since(quietSince) >= idle {
			return true
		}
		if !sleep() {
			return false
		}
	}
}
