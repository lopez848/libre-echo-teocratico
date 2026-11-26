package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// publicKey is the owner's Ed25519 public key (base64), set at build time:
// -ldflags "-X main.publicKey=<key>". Without it the agent installs nothing.
var publicKey string

// services lists the only programs a release may replace.
var services = map[string]Service{
	"puente": {Name: "puente", HealthURL: "http://127.0.0.1:18080/health"},
	"vigia":  {Name: "vigia"},
}

func main() {
	repo := flag.String("repo", "lopez848/libre-echo-teocratico", "GitHub repository with the signed releases")
	dir := flag.String("dir", "/data/libreecho/local/services", "local services directory")
	state := flag.String("state", "/data/libreecho/local/ota/state.json", "agent state file")
	every := flag.Duration("every", 6*time.Hour, "time between checks")
	first := flag.Duration("first", 10*time.Minute, "delay before the first check after start")
	once := flag.Bool("once", false, "check once and exit")
	flag.Parse()
	log.SetFlags(log.LstdFlags)

	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		log.Fatal("ota: built without a valid public key; refusing to run")
	}
	a := &Agent{
		Repo: *repo, API: "https://api.github.com", HTTP: &http.Client{Timeout: 2 * time.Minute},
		Pub: ed25519.PublicKey(pub), Services: services, Dir: *dir, StateFile: *state,
		Restart: restartService, Healthy: healthy,
	}
	check := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		got, err := a.Check(ctx)
		if err != nil {
			log.Printf("ota: %v", err)
			return
		}
		log.Printf("ota: %s", got)
	}
	if *once {
		check()
		return
	}
	jitter := func(d time.Duration) time.Duration { return d + time.Duration(rand.Int64N(int64(d/6)+1)) }
	time.Sleep(jitter(*first))
	for {
		check()
		time.Sleep(jitter(*every))
	}
}

// restartService stops the program; the LibreEcho local supervisor starts
// the new binary again by itself.
func restartService(name string) error {
	for _, pid := range pidsOf(name) {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return err
		}
	}
	return nil
}

func pidsOf(name string) []int {
	var out []int
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", d.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(b)) == name {
			out = append(out, pid)
		}
	}
	return out
}

// healthy waits for the restarted program: its health URL must answer, or
// the process must be running, and still be so after a settle time. A crash
// loop fails fast, before the supervisor gives up (10 failures/10 min).
func healthy(ctx context.Context, s Service) bool {
	up := func() bool {
		if s.HealthURL == "" {
			return len(pidsOf(s.Name)) > 0
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", s.HealthURL, nil)
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	time.Sleep(3 * time.Second) // let the supervisor start it
	return waitHealthy(ctx, healthWait{Up: up, PIDs: func() []int { return pidsOf(s.Name) },
		Poll: time.Second, Deadline: 60 * time.Second, Settle: 20 * time.Second, MaxPIDs: 3})
}
