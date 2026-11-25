package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Radio is the subset of libreecho-radiod the player uses.
type Radio interface {
	Play(url, title, station string) error
	Stop() error
	Pause() error
	Resume() error
	Status() (playing, paused bool, url string, err error)
}

// RadioSock talks to /run/libreecho/radio.sock:
// {"v":1,"id":N,"cmd":"play|stop|pause|resume|status","args":{...}}.
type RadioSock struct {
	Path string
	id   int64
}

func (r *RadioSock) call(cmd string, args map[string]any) (map[string]any, error) {
	c, err := net.DialTimeout("unix", r.Path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if args == nil {
		args = map[string]any{}
	}
	id := atomic.AddInt64(&r.id, 1) & 0x3fffffff
	b, _ := json.Marshal(map[string]any{"v": 1, "id": id, "cmd": cmd, "args": args})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("radiod %s: %s", cmd, resp.Error)
	}
	return resp.Data, nil
}

func (r *RadioSock) Play(url, title, station string) error {
	_, err := r.call("play", map[string]any{"url": url, "title": title, "station": station})
	return err
}
func (r *RadioSock) Stop() error   { _, err := r.call("stop", nil); return err }
func (r *RadioSock) Pause() error  { _, err := r.call("pause", nil); return err }
func (r *RadioSock) Resume() error { _, err := r.call("resume", nil); return err }
func (r *RadioSock) Status() (bool, bool, string, error) {
	d, err := r.call("status", nil)
	if err != nil {
		return false, false, "", err
	}
	playing, _ := d["playing"].(bool)
	paused, _ := d["paused"].(bool)
	url, _ := d["url"].(string)
	return playing, paused, url, nil
}

// Player plays a queue of tracks through radiod, advancing when a track
// ends. If playback stops early (someone stopped it elsewhere) the queue is
// dropped instead of skipping ahead.
type Player struct {
	Radio Radio
	Poll  time.Duration
	// AlwaysAdvance moves to the next track whenever one ends, even early
	// (a bad file must not end the whole list).
	AlwaysAdvance bool

	mu      sync.Mutex
	queue   []Track
	idx     int
	name    string
	active  bool
	started time.Time
	gen     int
}

var errNoQueue = errors.New("no queue")

func (p *Player) Start(name string, tracks []Track, shuffle bool) (Track, error) {
	if len(tracks) == 0 {
		return Track{}, errNoQueue
	}
	q := append([]Track(nil), tracks...)
	if shuffle {
		rand.Shuffle(len(q), func(i, j int) { q[i], q[j] = q[j], q[i] })
	}
	p.mu.Lock()
	p.queue, p.idx, p.name, p.active = q, 0, name, true
	p.gen++
	gen := p.gen
	err := p.playLocked()
	t := p.queue[0]
	p.mu.Unlock()
	if err != nil {
		p.Stop()
		return Track{}, err
	}
	go p.watch(gen)
	return t, nil
}

func (p *Player) playLocked() error {
	t := p.queue[p.idx]
	p.started = time.Now()
	return p.Radio.Play(t.URL, t.Title, p.name)
}

// Next skips to the following track; ok=false when nothing is queued.
func (p *Player) Next(delta int) (Track, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return Track{}, false, nil
	}
	n := p.idx + delta
	if n < 0 {
		n = 0
	}
	if n >= len(p.queue) {
		p.active = false
		return Track{}, true, p.Radio.Stop()
	}
	p.idx = n
	return p.queue[n], true, p.playLocked()
}

func (p *Player) Current() (Track, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return Track{}, "", false
	}
	return p.queue[p.idx], p.name, true
}

func (p *Player) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

func (p *Player) Stop() {
	p.mu.Lock()
	was := p.active
	p.active = false
	p.gen++
	p.mu.Unlock()
	if was {
		_ = p.Radio.Stop()
	}
}

func (p *Player) watch(gen int) {
	poll := p.Poll
	if poll == 0 {
		poll = 2 * time.Second
	}
	quiet := 0
	for {
		time.Sleep(poll)
		playing, paused, _, err := p.Radio.Status()
		p.mu.Lock()
		if p.gen != gen || !p.active {
			p.mu.Unlock()
			return
		}
		if err != nil || playing || paused {
			quiet = 0
			p.mu.Unlock()
			continue
		}
		quiet++
		if quiet < 2 { // tolerate a gap while a new track connects
			p.mu.Unlock()
			continue
		}
		quiet = 0
		t := p.queue[p.idx]
		elapsed := time.Since(p.started).Seconds()
		if !p.AlwaysAdvance && t.Duration > 0 && elapsed < t.Duration*0.85-5 {
			logf("player: stopped early (%.0fs of %.0fs), dropping queue", elapsed, t.Duration)
			p.active = false
			p.mu.Unlock()
			return
		}
		if p.idx+1 >= len(p.queue) {
			p.active = false
			p.mu.Unlock()
			return
		}
		p.idx++
		if err := p.playLocked(); err != nil {
			logf("player: %v", err)
		}
		p.mu.Unlock()
	}
}
