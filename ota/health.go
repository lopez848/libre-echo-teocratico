package main

import (
	"context"
	"time"
)

// healthWait describes how to wait for a restarted service.
type healthWait struct {
	Up       func() bool
	PIDs     func() []int
	Poll     time.Duration
	Deadline time.Duration // time allowed to come up
	Settle   time.Duration // must still be up after this
	MaxPIDs  int           // this many different processes means a crash loop
}

func waitHealthy(ctx context.Context, h healthWait) bool {
	seen := map[int]bool{}
	crashLoop := func() bool {
		for _, p := range h.PIDs() {
			seen[p] = true
		}
		return len(seen) >= h.MaxPIDs
	}
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	deadline := time.Now().Add(h.Deadline)
	for !h.Up() {
		if crashLoop() || time.Now().After(deadline) || !wait(h.Poll) {
			return false
		}
	}
	for end := time.Now().Add(h.Settle); time.Now().Before(end); {
		if crashLoop() || !wait(h.Poll) {
			return false
		}
	}
	return !crashLoop() && h.Up()
}
