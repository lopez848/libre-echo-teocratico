package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseDailyText(t *testing.T) {
	page, err := os.ReadFile("testdata/wol-2026-11-25.html")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDailyText(string(page), "2026-11-25")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Miércoles 25 de noviembre.", "Que la gente sepa que tu nombre es Jehová", "(Salmo 83, versículo 18)", "(Isaías 43, versículos 10 al 12)", "testigos de Jehová"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, bad := range []string{"<", "w24", "párr", "Jueves 26", "Examinemos"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %q in %q", bad, got)
		}
	}
	if _, err := ParseDailyText(string(page), "2026-12-31"); err == nil {
		t.Error("missing date must fail")
	}
}

func TestSpeakBibleRefs(t *testing.T) {
	cases := map[string]string{
		"(Sal. 73:23-28)": "(Salmo 73, versículos 23 al 28)",
		"(1 Juan 4:8)":    "(Primera de Juan 4, versículo 8)",
		"Juan 3:16":       "Juan 3, versículo 16",
		"Rom. 10:13, 14":  "Romanos 10, versículos 13 y 14",
	}
	for in, want := range cases {
		if got := SpeakBibleRefs(in); got != want {
			t.Errorf("SpeakBibleRefs(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSplitSentences(t *testing.T) {
	text := strings.Repeat("Esta es una frase de prueba. ", 30)
	parts := SplitSentences(text, 120)
	if len(parts) < 5 {
		t.Fatalf("parts=%d", len(parts))
	}
	for _, p := range parts {
		if len([]rune(p)) > 120 || p == "" {
			t.Fatalf("bad part %q", p)
		}
	}
	if strings.Join(parts, " ") != strings.TrimSpace(text) {
		t.Fatal("text changed by splitting")
	}
	if got := SplitSentences("Hola.", 300); len(got) != 1 || got[0] != "Hola." {
		t.Fatalf("got %q", got)
	}
}

type fakeRadio struct {
	mu      sync.Mutex
	played  []string
	playing bool
	stops   int
}

func (f *fakeRadio) Play(url, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.played, f.playing = append(f.played, url), true
	return nil
}
func (f *fakeRadio) Stop() error {
	f.mu.Lock()
	f.playing = false
	f.stops++
	f.mu.Unlock()
	return nil
}
func (f *fakeRadio) Pause() error  { return nil }
func (f *fakeRadio) Resume() error { return nil }
func (f *fakeRadio) Status() (bool, bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.playing, false, "", nil
}
func (f *fakeRadio) end()       { f.mu.Lock(); f.playing = false; f.mu.Unlock() }
func (f *fakeRadio) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.played) }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestPlayerAdvancesWhenTrackEnds(t *testing.T) {
	r := &fakeRadio{}
	p := &Player{Radio: r, Poll: 5 * time.Millisecond}
	tracks := []Track{{Title: "a", URL: "u1"}, {Title: "b", URL: "u2"}}
	if _, err := p.Start("Lista", tracks, false); err != nil {
		t.Fatal(err)
	}
	r.end()
	waitFor(t, func() bool { return r.count() == 2 })
	if cur, _, _ := p.Current(); cur.URL != "u2" {
		t.Fatalf("current=%+v", cur)
	}
	r.end()
	waitFor(t, func() bool { return !p.Active() })
}

func TestPlayerDropsQueueWhenStoppedEarly(t *testing.T) {
	r := &fakeRadio{}
	p := &Player{Radio: r, Poll: 5 * time.Millisecond}
	p.Start("Lista", []Track{{URL: "u1", Duration: 240}, {URL: "u2", Duration: 240}}, false)
	r.end() // stopped after a few ms of a 4-minute song
	waitFor(t, func() bool { return !p.Active() })
	if r.count() != 1 {
		t.Fatalf("must not skip ahead, played=%v", r.played)
	}
}

func TestPlayJWMusicAndNowPlaying(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/categories/S/AudioOriginalSongs") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"category":{"name":"Canciones originales","media":[{"title":"Uno","duration":200,"files":[{"mimetype":"audio/mpeg","progressiveDownloadURL":"https://x/1.mp3"}]},{"title":"Dos","duration":200,"files":[{"mimetype":"video/mp4","progressiveDownloadURL":"v"},{"mimetype":"audio/mpeg","progressiveDownloadURL":"https://x/2.mp3"}]}]}}`)
	}))
	defer srv.Close()
	r := &fakeRadio{}
	old := *svc
	defer func() { *svc = old }()
	svc.JW = &JW{HTTP: srv.Client(), Mediator: srv.URL}
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	got := Exec(&fakeAPI{}, "play_jw_music", `{"collection":"las canciones originales"}`)
	if !strings.HasPrefix(got, "Poniendo Canciones originales de jw.org en orden aleatorio. Empieza ") || len(r.played) != 1 {
		t.Fatalf("reply=%q played=%v", got, r.played)
	}
	first := r.played[0]
	cur, _, _ := svc.Player.Current()
	if got := Exec(&fakeAPI{}, "now_playing", ``); got != "Está sonando "+cur.Title+", de Canciones originales." {
		t.Fatalf("now_playing=%q", got)
	}
	if got := Exec(&fakeAPI{}, "media_control", `{"action":"next"}`); !strings.HasPrefix(got, "Poniendo ") || r.played[1] == first {
		t.Fatalf("next=%q played=%v", got, r.played)
	}
	Exec(&fakeAPI{}, "stop", ``)
	if svc.Player.Active() || r.stops == 0 {
		t.Fatal("stop must end the queue")
	}
}

func TestDailyTextToolUsesLocalDate(t *testing.T) {
	page, _ := os.ReadFile("testdata/wol-2026-11-25.html")
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write(page)
	}))
	defer srv.Close()
	old := *svc
	defer func() { *svc = old }()
	loc, _ := time.LoadLocation("America/Chicago")
	svc.JW = &JW{HTTP: srv.Client(), WOLBase: srv.URL}
	svc.Location = loc
	// 05:30 UTC on the 26th is still the evening of the 25th in Chicago.
	svc.Now = func() time.Time { return time.Date(2026, 11, 26, 5, 30, 0, 0, time.UTC) }
	got := Exec(&fakeAPI{}, "daily_text", `{}`)
	if path != "/es/wol/h/r4/lp-s/2026/11/25" || !strings.HasPrefix(got, "Miércoles 25 de noviembre.") {
		t.Fatalf("path=%q got=%.80q", path, got)
	}
}

func TestMatchJWCollection(t *testing.T) {
	cases := map[string]string{
		"Música internacional":                            "AudioInternationalMusic",
		"las canciones Música internacional":              "AudioInternationalMusic",
		"Cantemos con gozo a Jehová (para las reuniones)": "SJJMeetings",
		"cantemos con gozo":                               "SJJChorus",
		"Cantemos con gozo a Jehová (instrumental)":       "SJJInstrumental",
		"Cantemos a Jehová (coro y orquesta)":             "Vocal",
		"canciones para niños":                            "AudioChildrenSongs",
		"Melodías del Reino":                              "KingdomMelodies",
		"Canciones originales":                            "AudioOriginalSongs",
	}
	for in, want := range cases {
		if got, ok := MatchJWCollection(in); !ok || got != want {
			t.Errorf("MatchJWCollection(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := MatchJWCollection("rock pesado"); ok {
		t.Error("unknown section must not match")
	}
}

func TestPlayerAlwaysAdvanceSkipsBadTrack(t *testing.T) {
	r := &fakeRadio{}
	p := &Player{Radio: r, Poll: 5 * time.Millisecond, AlwaysAdvance: true}
	p.Start("Lista", []Track{{URL: "u1", Duration: 240}, {URL: "u2", Duration: 240}}, false)
	r.end() // ends after a few ms (bad file)
	waitFor(t, func() bool { return r.count() == 2 })
}
