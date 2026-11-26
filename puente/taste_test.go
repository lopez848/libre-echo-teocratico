package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func urls(ts []Track) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.URL)
	}
	return strings.Join(s, ",")
}

func TestTasteRecordsPersistsAndFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taste.json")
	a := Track{URL: "yt:a", Artist: "Uno"}
	b := Track{URL: "yt:b", Artist: "Dos"}
	tt := LoadTaste(path)
	tt.Record(a, false)
	tt.Record(a, false)
	tt.Record(b, true)
	tt.Record(Track{URL: "https://x/1.mp3", Artist: "JW"}, false) // not YouTube: ignored

	re := LoadTaste(path) // reload from disk
	got := re.Filter([]Track{a, b, {URL: "yt:c", Artist: "Tres"}}, "")
	if urls(got) != "yt:b,yt:c" {
		t.Fatalf("a song skipped twice must be dropped: %s", urls(got))
	}
	if re.Songs["https://x/1.mp3"] != nil {
		t.Fatal("non-YouTube tracks must not be recorded")
	}
	// One more full play outweighs the skips.
	re.Record(a, true)
	re.Record(a, true)
	re.Record(a, true)
	if urls(re.Filter([]Track{a}, "")) != "yt:a" {
		t.Fatal("a song played more than skipped stays")
	}
}

func TestTasteDropsSkippedArtistUnlessRequested(t *testing.T) {
	tt := LoadTaste(filepath.Join(t.TempDir(), "taste.json"))
	for i := 0; i < 3; i++ {
		tt.Record(Track{URL: "yt:m" + string(rune('0'+i)), Artist: "Maluma"}, false)
	}
	list := []Track{{URL: "yt:x", Artist: "MALUMA"}, {URL: "yt:y", Artist: "Fonseca"}}
	if got := urls(tt.Filter(list, "")); got != "yt:y" {
		t.Fatalf("an artist skipped 3 times must be dropped: %s", got)
	}
	if got := urls(tt.Filter(list, "maluma")); got != "yt:x,yt:y" {
		t.Fatalf("the artist the owner asked for stays: %s", got)
	}
	if got := urls(tt.Filter([]Track{{URL: "yt:x", Artist: "Maluma"}}, "")); got != "yt:x" {
		t.Fatalf("filtering must never empty the list: %s", got)
	}
}

func TestTastePrefersLikedArtists(t *testing.T) {
	tt := LoadTaste(filepath.Join(t.TempDir(), "taste.json"))
	tt.Record(Track{URL: "yt:old", Artist: "Fonseca"}, true)
	tt.Record(Track{URL: "yt:old2", Artist: "Fonseca"}, true)
	list := []Track{{URL: "yt:1", Artist: "Otro"}, {URL: "yt:2", Artist: "Fonseca"}, {URL: "yt:3", Artist: "Nuevo"}}
	if got := urls(tt.Rank(list)); got != "yt:2,yt:1,yt:3" {
		t.Fatalf("liked artists first, the rest in order: %s", got)
	}
}

func TestPlayerReportsSkipsAndFinishes(t *testing.T) {
	type ev struct {
		url      string
		finished bool
	}
	var got []ev
	r := &fakeRadio{}
	p := &Player{Radio: r, Poll: 5 * time.Millisecond, AlwaysAdvance: true,
		OnEnd: func(t Track, finished bool) { got = append(got, ev{t.URL, finished}) }}
	p.Start("x", []Track{{URL: "yt:1", Duration: 200}, {URL: "yt:2"}, {URL: "yt:3"}}, false)
	p.Next(1) // skipped after a moment of a 200 s song
	r.end()   // yt:2 has no known length: ending counts as played
	waitFor(t, func() bool { return r.count() == 3 })
	if len(got) != 2 || got[0] != (ev{"yt:1", false}) || got[1] != (ev{"yt:2", true}) {
		t.Fatalf("events %+v", got)
	}
}

func TestPlayYouTubeAppliesTaste(t *testing.T) {
	srv, _ := fakeInnertube(t, map[string]string{"/search": searchAnswer, "/next": musicMixAnswer})
	r := withYouTube(t, &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL})
	svc.Taste = LoadTaste(filepath.Join(t.TempDir(), "taste.json"))
	for i := 0; i < 3; i++ {
		svc.Taste.Record(Track{URL: "yt:old" + string(rune('0'+i)), Artist: "Maluma"}, false)
	}
	Exec(&fakeAPI{}, "play_youtube", `{"query":"bachata rosa","mode":"song"}`)
	Exec(&fakeAPI{}, "media_control", `{"action":"next"}`)
	Exec(&fakeAPI{}, "media_control", `{"action":"next"}`)
	if strings.Contains(strings.Join(r.played, ","), "yt:m2") {
		t.Fatalf("a disliked artist must not be queued: %v", r.played)
	}
}
