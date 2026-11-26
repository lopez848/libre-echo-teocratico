package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Taste learns from what the owner does with YouTube songs: a song that
// plays to the end counts as liked, one skipped with "siguiente" counts as
// disliked. It is kept in a small JSON file on the device.
type Taste struct {
	Path    string                 `json:"-"`
	Songs   map[string]*TasteScore `json:"songs"`   // by yt:<id>
	Artists map[string]*TasteScore `json:"artists"` // by folded artist name
	mu      sync.Mutex
}

type TasteScore struct {
	Played  int `json:"played"`
	Skipped int `json:"skipped"`
}

// LoadTaste reads the taste file; a missing or broken file starts empty.
func LoadTaste(path string) *Taste {
	t := &Taste{Path: path}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, t); err != nil {
			logf("taste: %v (starting empty)", err)
		}
	}
	if t.Songs == nil {
		t.Songs = map[string]*TasteScore{}
	}
	if t.Artists == nil {
		t.Artists = map[string]*TasteScore{}
	}
	return t
}

func bump(m map[string]*TasteScore, key string, finished bool) {
	if key == "" {
		return
	}
	s := m[key]
	if s == nil {
		s = &TasteScore{}
		m[key] = s
	}
	if finished {
		s.Played++
	} else {
		s.Skipped++
	}
}

// Record notes a YouTube track that played to the end or was skipped.
func (t *Taste) Record(tr Track, finished bool) {
	if t == nil || !strings.HasPrefix(tr.URL, "yt:") {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	bump(t.Songs, tr.URL, finished)
	bump(t.Artists, fold(tr.Artist), finished)
	t.save()
}

func (t *Taste) save() {
	if t.Path == "" {
		return
	}
	b, err := json.Marshal(t)
	if err != nil {
		return
	}
	tmp := t.Path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		logf("taste: %v", err)
		return
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		logf("taste: %v", err)
		return
	}
	if err := os.Rename(tmp, t.Path); err != nil {
		logf("taste: %v", err)
	}
}

func (s *TasteScore) dislikedSong() bool { return s != nil && s.Skipped >= 2 && s.Skipped > s.Played }
func (s *TasteScore) dislikedArtist() bool {
	return s != nil && s.Skipped >= 3 && s.Skipped > 2*s.Played
}

// Filter drops disliked songs and artists, except the artist the owner asked
// for. It never empties the list.
func (t *Taste) Filter(tracks []Track, keepArtist string) []Track {
	if t == nil {
		return tracks
	}
	keep := fold(keepArtist)
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Track
	for _, tr := range tracks {
		artist := fold(tr.Artist)
		requested := keep != "" && strings.Contains(artist, keep)
		if t.Songs[tr.URL].dislikedSong() {
			continue
		}
		if !requested && t.Artists[artist].dislikedArtist() {
			continue
		}
		out = append(out, tr)
	}
	if len(out) == 0 {
		return tracks
	}
	return out
}

// Rank moves songs of artists the owner usually plays to the end first,
// keeping the original order otherwise.
func (t *Taste) Rank(tracks []Track) []Track {
	if t == nil {
		return tracks
	}
	t.mu.Lock()
	score := func(tr Track) int {
		if s := t.Artists[fold(tr.Artist)]; s != nil {
			return s.Played - s.Skipped
		}
		return 0
	}
	out := append([]Track(nil), tracks...)
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) > score(out[j]) })
	t.mu.Unlock()
	return out
}
