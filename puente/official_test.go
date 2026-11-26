package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const pubMediaArticle = `{"files":{"S":{"MP3":[{"title":"Un artículo de prueba (1-7 de diciembre)","docid":222,"duration":1244.5,
 "file":{"url":"https://a/w_S_test.mp3"},
 "markers":{"markers":[{"startTime":"00:00:00.000","duration":"00:00:06.425","mepsParagraphId":1},
  {"startTime":"00:00:20.000","duration":"00:00:05.000","mepsParagraphId":40},
  {"startTime":"00:00:30.500","duration":"00:00:04.000","mepsParagraphId":41},
  {"startTime":"00:01:15.250","duration":"00:00:04.000","mepsParagraphId":42}]}}]}}}`

const pubMediaChapter = `{"files":{"S":{"MP3":[{"title":"Capítulo 3","duration":285,"file":{"url":"https://a/nwt_43_3.mp3"},
 "markers":{"markers":[{"verseNumber":15,"startTime":"00:02:00.000","duration":"00:00:08.000"},
  {"verseNumber":16,"startTime":"00:02:08.500","duration":"00:00:10.250"},
  {"verseNumber":17,"startTime":"00:02:18.750","duration":"00:00:07.000"}]}}]}}}`

// fakeJWOrg serves wol pages and pub-media answers; noAudio makes
// pub-media answer 404 like a publication without recordings.
func fakeJWOrg(t *testing.T, noAudio bool) *JW {
	t.Helper()
	j, _ := fakeWOL(t)
	pm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case noAudio:
			http.Error(w, `{"status":404}`, http.StatusNotFound)
		case q.Get("docid") == "222":
			w.Write([]byte(pubMediaArticle))
		case q.Get("pub") == "nwt" && q.Get("booknum") == "43" && q.Get("track") == "3":
			w.Write([]byte(pubMediaChapter))
		default:
			http.Error(w, `{"status":404}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(pm.Close)
	j.PubMedia = pm.URL
	return j
}

func withOfficial(t *testing.T, noAudio bool) *fakeRadio {
	t.Helper()
	old := *svc
	t.Cleanup(func() { svc.Reader.Stop(); *svc = old })
	loc, _ := time.LoadLocation("America/Chicago")
	svc.JW, svc.Location = fakeJWOrg(t, noAudio), loc
	svc.Now = func() time.Time { return time.Date(2026, 11, 26, 20, 0, 0, 0, loc) }
	r := &fakeRadio{}
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	svc.Reader = &Reader{Voice: &fakeVoice{}, Poll: 2 * time.Millisecond, Idle: 5 * time.Millisecond, StartWait: 5 * time.Millisecond}
	return r
}

func TestParseMediaTime(t *testing.T) {
	for in, want := range map[string]float64{"00:00:06.425": 6.425, "00:01:15.250": 75.25, "01:00:00.000": 3600, "bad": 0} {
		if got := parseMediaTime(in); got != want {
			t.Errorf("parseMediaTime(%q)=%v want %v", in, got, want)
		}
	}
}

func TestWatchtowerPlaysTheOfficialAudio(t *testing.T) {
	r := withOfficial(t, false)
	got := Exec(&fakeAPI{}, "read_watchtower", `{}`)
	if got != "Pongo el audio del estudio de La Atalaya de esta semana: Un artículo de prueba." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return r.count() == 1 })
	if r.played[0] != "https://a/w_S_test.mp3" {
		t.Fatalf("played %v", r.played)
	}
	// "lee el párrafo 2" jumps to the question of paragraph 2 (pid 41).
	if got := Exec(&fakeAPI{}, "read_paragraph", `{"paragraph":2}`); got != "Leyendo el párrafo 2 de Un artículo de prueba." {
		t.Fatalf("paragraph reply %q", got)
	}
	waitFor(t, func() bool { return r.count() == 2 })
	if r.played[1] != "https://a/w_S_test.mp3#t=30.5" {
		t.Fatalf("jump played %v", r.played)
	}
	// Paragraphs 3 and 4 share one question (pid 42).
	Exec(&fakeAPI{}, "read_paragraph", `{"paragraph":4}`)
	waitFor(t, func() bool { return r.count() == 3 })
	if r.played[2] != "https://a/w_S_test.mp3#t=75.25" {
		t.Fatalf("shared question jump %v", r.played)
	}
}

func TestWatchtowerFallsBackToTheVoiceWithoutAudio(t *testing.T) {
	r := withOfficial(t, true)
	got := Exec(&fakeAPI{}, "read_watchtower", `{}`)
	if got != "Voy a leer el estudio de La Atalaya de esta semana: Un artículo de prueba." {
		t.Fatalf("reply %q", got)
	}
	if r.count() != 0 || !svc.Reader.Loaded() {
		t.Fatalf("without audio the voice reads it: played=%v", r.played)
	}
}

func TestBibleVersesPlayTheirPartOfTheChapterAudio(t *testing.T) {
	r := withOfficial(t, false)
	got := Exec(&fakeAPI{}, "read_bible", `{"book":"Juan","chapter":3,"verse_start":16}`)
	if got != "Juan 3, versículo 16." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return r.count() == 1 })
	if r.played[0] != "https://a/nwt_43_3.mp3#t=128.5,138.75" {
		t.Fatalf("verse 16 only: %v", r.played)
	}
	Exec(&fakeAPI{}, "read_bible", `{"book":"Juan","chapter":3,"verse_start":15,"verse_end":17}`)
	waitFor(t, func() bool { return r.count() == 2 })
	if r.played[1] != "https://a/nwt_43_3.mp3#t=120,145.75" {
		t.Fatalf("verses 15-17: %v", r.played)
	}
	Exec(&fakeAPI{}, "read_bible", `{"book":"Juan","chapter":3}`)
	waitFor(t, func() bool { return r.count() == 3 })
	if r.played[2] != "https://a/nwt_43_3.mp3" {
		t.Fatalf("whole chapter: %v", r.played)
	}
}

func TestMediaFragment(t *testing.T) {
	cases := []struct {
		in, url    string
		start, end float64
	}{
		{"https://a/x.mp3", "https://a/x.mp3", 0, 0},
		{"https://a/x.mp3#t=30.5", "https://a/x.mp3", 30.5, 0},
		{"https://a/x.mp3#t=120,145.75", "https://a/x.mp3", 120, 145.75},
	}
	for _, c := range cases {
		u, s, e := splitMediaFragment(c.in)
		if u != c.url || s != c.start || e != c.end {
			t.Errorf("%q -> %q %v %v", c.in, u, s, e)
		}
	}
}

func TestBusPlayerPlaysOnlyTheFragment(t *testing.T) {
	file := m4aTone(t, 2) // one second
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.m4a", time.Time{}, strings.NewReader(string(file)))
	}))
	defer srv.Close()
	bus := filepath.Join(t.TempDir(), "media.pcm")
	os.WriteFile(bus, nil, 0o644)
	b := &BusPlayer{Bus: bus, HTTP: srv.Client()}
	if err := b.stream(context.Background(), srv.URL+"/a.m4a#t=0.25,0.75"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(bus)
	frames, _ := pcmStats(out)
	if frames < 22000 || frames > 26000 {
		t.Fatalf("frames=%d, want about half a second at 48 kHz", frames)
	}
	_ = fmt.Sprint
}
