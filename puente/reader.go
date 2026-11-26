package main

import (
	"context"
	"sync"
	"time"
)

// Reader reads a long Reading aloud through ttsd after agentd's intro, one
// piece at a time. Stop keeps the position: Resume repeats the piece that
// was interrupted and goes on; Jump starts at a paragraph.
type Reader struct {
	Voice     voice
	Poll      time.Duration
	Idle      time.Duration
	StartWait time.Duration
	Pause     time.Duration // silence before pieces marked Pause

	mu      sync.Mutex
	cur     *Reading
	pos     int
	gen     int
	cancel  context.CancelFunc
	running bool
}

// Start replaces any reading in progress and reads rd from piece from.
func (r *Reader) Start(rd Reading, from int) {
	r.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cur, r.pos = &rd, from
	r.launch()
}

// Interrupt stops the reading for a new turn and returns a token for
// ResumeIfUntouched; ok is false when nothing was being read.
func (r *Reader) Interrupt() (token int, ok bool) {
	if r == nil {
		return 0, false
	}
	was := r.Active()
	r.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen, was
}

// ResumeIfUntouched resumes after a turn that did not start, stop or move
// the reading itself (a quick question, or a false wake word).
func (r *Reader) ResumeIfUntouched(token int) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen != token || r.cur == nil || r.pos >= len(r.cur.Pieces) {
		return false
	}
	r.launch()
	return true
}

// Stop ends the reading and keeps its position.
func (r *Reader) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.gen++
	r.running = false
}

// Loaded reports whether a reading has been started at least once.
func (r *Reader) Loaded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur != nil
}

// Forget stops the reading and drops it, so nothing can be resumed.
func (r *Reader) Forget() {
	if r == nil {
		return
	}
	r.Stop()
	r.mu.Lock()
	r.cur = nil
	r.mu.Unlock()
}

// Active reports whether a reading is being spoken (or about to be).
func (r *Reader) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Resume continues the last reading; para is where it picks up.
func (r *Reader) Resume() (title string, para int, ok bool) {
	r.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil || r.pos >= len(r.cur.Pieces) {
		return "", 0, false
	}
	r.launch()
	return r.cur.Title, r.cur.Pieces[r.pos].Para, true
}

// Jump restarts the last reading at the first piece of paragraph para.
func (r *Reader) Jump(para int) (string, bool) {
	r.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return "", false
	}
	for i, p := range r.cur.Pieces {
		if p.Para == para {
			r.pos = i
			r.launch()
			return r.cur.Title, true
		}
	}
	return "", false
}

// launch starts the speaking goroutine; r.mu must be held.
func (r *Reader) launch() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.running = cancel, true
	gen := r.gen
	go func() {
		defer cancel()
		finish := func() {
			r.mu.Lock()
			if r.gen == gen {
				r.running = false
			}
			r.mu.Unlock()
		}
		if !waitSilence(ctx, r.Voice, r.Poll, r.Idle, r.StartWait) {
			finish()
			return
		}
		for {
			r.mu.Lock()
			if ctx.Err() != nil || r.gen != gen || r.pos >= len(r.cur.Pieces) {
				r.mu.Unlock()
				finish()
				return
			}
			piece := r.cur.Pieces[r.pos]
			r.mu.Unlock()
			if piece.Pause && r.Pause > 0 {
				select {
				case <-ctx.Done():
					finish()
					return
				case <-time.After(r.Pause):
				}
			}
			for _, s := range SplitSentences(piece.Text, 200) {
				if ctx.Err() != nil {
					finish()
					return
				}
				if err := r.Voice.Speak(Speakable(s)); err != nil {
					logf("reader voice failed: %v", err)
					finish()
					return
				}
			}
			r.mu.Lock()
			if ctx.Err() == nil && r.gen == gen {
				r.pos++
			}
			r.mu.Unlock()
		}
	}()
}
