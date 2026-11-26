package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeInnertube serves canned innertube answers per endpoint and records
// the client name each request used.
func fakeInnertube(t *testing.T, answers map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var clients []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Context struct {
				Client struct {
					ClientName string `json:"clientName"`
				} `json:"client"`
			} `json:"context"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		clients = append(clients, body.Context.Client.ClientName+" "+r.URL.Path)
		for path, a := range answers {
			if strings.HasSuffix(r.URL.Path, path) {
				io.WriteString(w, a)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &clients
}

const searchAnswer = `{"contents":{"x":[
 {"videoRenderer":{"videoId":"long1","title":{"runs":[{"text":"Juan Luis Guerra sus mejores éxitos"}]},"ownerText":{"runs":[{"text":"Voces del Recuerdo"}]},"lengthText":{"simpleText":"1:05:00"}}},
 {"videoRenderer":{"videoId":"song1","title":{"runs":[{"text":"Juan Luis Guerra - Bachata Rosa"}]},"ownerText":{"runs":[{"text":"Juan Luis Guerra"}]},"lengthText":{"simpleText":"3:45"}}},
 {"videoRenderer":{"videoId":"live1","title":{"runs":[{"text":"Juan Luis Guerra en vivo ahora"}]},"ownerText":{"runs":[{"text":"Juan Luis Guerra"}]}}},
 {"videoRenderer":{"videoId":"song2","title":{"runs":[{"text":"Burbujas de amor"}]},"ownerText":{"runs":[{"text":"Juan Luis Guerra 4.40"}]},"lengthText":{"simpleText":"4:10"}}}
]}}`

func TestYouTubeSearchKeepsSongLengthVideos(t *testing.T) {
	srv, clients := fakeInnertube(t, map[string]string{"/search": searchAnswer})
	y := &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL}
	got, err := y.Search("Juan Luis Guerra")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].URL != "yt:song1" || got[1].URL != "yt:song2" {
		t.Fatalf("compilations and lives must be dropped: %+v", got)
	}
	if got[0].Artist != "Juan Luis Guerra" || got[0].Duration != 225 || got[0].Title != "Juan Luis Guerra - Bachata Rosa" {
		t.Fatalf("track fields: %+v", got[0])
	}
	if len(*clients) != 1 || (*clients)[0] != "WEB /youtubei/v1/search" {
		t.Fatalf("search must use the WEB client: %v", *clients)
	}
}

const musicMixAnswer = `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"musicQueueRenderer":{"content":{"playlistPanelRenderer":{"contents":[
 {"playlistPanelVideoRenderer":{"videoId":"seed","title":{"runs":[{"text":"Provenza"}]},"shortBylineText":{"runs":[{"text":"KAROL G"}]},"lengthText":{"runs":[{"text":"3:30"}]}}},
 {"playlistPanelVideoRenderer":{"videoId":"m2","title":{"runs":[{"text":"Según Quién"}]},"longBylineText":{"runs":[{"text":"Maluma"},{"text":" • "},{"text":"Papi Juancho"}]},"lengthText":{"runs":[{"text":"3:05"}]}}},
 {"automixPreviewVideoRenderer":{"content":{}}}
]}}}}}}]}}}}}`

func TestYouTubeSimilarUsesMusicMix(t *testing.T) {
	srv, clients := fakeInnertube(t, map[string]string{"/next": musicMixAnswer})
	y := &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL}
	got, err := y.Similar("seed")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].URL != "yt:m2" || got[1].Artist != "Maluma" || got[1].Duration != 185 || got[0].Duration != 210 {
		t.Fatalf("got %+v", got)
	}
	if (*clients)[0] != "WEB_REMIX /youtubei/v1/next" {
		t.Fatalf("similar songs come from YouTube Music: %v", *clients)
	}
}

const webMixAnswer = `{"contents":{"twoColumnWatchNextResults":{"playlist":{"playlist":{"contents":[
 {"playlistPanelVideoRenderer":{"videoId":"song1","title":{"simpleText":"Bachata Rosa"},"shortBylineText":{"runs":[{"text":"Juan Luis Guerra"}]},"lengthText":{"simpleText":"3:45"}}},
 {"playlistPanelVideoRenderer":{"videoId":"other","title":{"simpleText":"Otra cosa"},"shortBylineText":{"runs":[{"text":"Romeo Santos"}]},"lengthText":{"simpleText":"4:00"}}},
 {"playlistPanelVideoRenderer":{"videoId":"song3","title":{"simpleText":"Ojalá que llueva café"},"shortBylineText":{"runs":[{"text":"JUAN LUIS GUERRA 4.40"}]},"lengthText":{"simpleText":"4:20"}}}
]}}}}}`

func TestYouTubeArtistKeepsOnlyThatArtist(t *testing.T) {
	srv, clients := fakeInnertube(t, map[string]string{"/search": searchAnswer, "/next": webMixAnswer})
	y := &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL}
	got, err := y.Artist("juan luis guerra")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tr := range got {
		ids = append(ids, tr.URL)
		if !strings.Contains(fold(tr.Artist), "juan luis guerra") {
			t.Fatalf("other artist kept: %+v", tr)
		}
	}
	// Mix of the first song plus search hits, without repeats.
	if strings.Join(ids, ",") != "yt:song1,yt:song3,yt:song2" {
		t.Fatalf("ids %v", ids)
	}
	if strings.Join(*clients, ";") != "WEB /youtubei/v1/search;WEB /youtubei/v1/next" {
		t.Fatalf("requests %v", *clients)
	}
}

func TestYouTubeArtistNotFound(t *testing.T) {
	srv, _ := fakeInnertube(t, map[string]string{"/search": searchAnswer, "/next": webMixAnswer})
	y := &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL}
	if got, err := y.Artist("metallica"); err == nil {
		t.Fatalf("an artist with no songs must be an error: %+v", got)
	}
}

func TestYouTubeAudioURLUsesProducerClient(t *testing.T) {
	var hdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Get("X-YouTube-Client-Name")
		b, _ := io.ReadAll(r.Body)
		if !bytes.Contains(b, []byte(`"IOS_PRODUCER"`)) || !bytes.Contains(b, []byte(`"videoId":"abc"`)) {
			t.Errorf("body %s", b)
		}
		io.WriteString(w, `{"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[
			{"itag":251,"mimeType":"audio/webm; codecs=\"opus\""},
			{"itag":137,"mimeType":"video/mp4","url":"https://v/video"},
			{"itag":140,"mimeType":"audio/mp4; codecs=\"mp4a.40.2\"","url":"https://a/audio140"}]}}`)
	}))
	defer srv.Close()
	y := &YouTube{HTTP: srv.Client(), Web: srv.URL}
	u, err := y.AudioURL("abc")
	if err != nil || u != "https://a/audio140" || hdr != "92" {
		t.Fatalf("url=%q err=%v client=%q", u, err, hdr)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in to confirm"}}`)
	}))
	defer bad.Close()
	y.Web = bad.URL
	if _, err := y.AudioURL("abc"); err == nil || !strings.Contains(err.Error(), "LOGIN_REQUIRED") {
		t.Fatalf("unplayable video must say why: %v", err)
	}
}

func TestRangeReaderFetchesChunksAndSeeks(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789"), 500) // 5000 bytes
	var plain int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			plain++
		}
		http.ServeContent(w, r, "a.m4a", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	rr := &rangeReader{HTTP: srv.Client(), URL: srv.URL, Chunk: 1000}
	got, err := io.ReadAll(rr)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read %d bytes, err %v", len(got), err)
	}
	if plain != 0 {
		t.Fatalf("every request must carry a Range header (plain GETs are throttled): %d", plain)
	}
	if n, err := rr.Seek(-500, io.SeekEnd); n != 4500 || err != nil {
		t.Fatalf("seek %d %v", n, err)
	}
	tail, _ := io.ReadAll(rr)
	if !bytes.Equal(tail, data[4500:]) {
		t.Fatalf("tail %q", tail[:20])
	}
	rr.Seek(10, io.SeekStart)
	buf := make([]byte, 5)
	io.ReadFull(rr, buf)
	if string(buf) != "01234" {
		t.Fatalf("after seek got %q", buf)
	}
}

func withYouTube(t *testing.T, y *YouTube) *fakeRadio {
	t.Helper()
	old := *svc
	t.Cleanup(func() { *svc = old })
	r := &fakeRadio{}
	svc.YT = y
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	return r
}

func TestPlayYouTubeArtist(t *testing.T) {
	srv, _ := fakeInnertube(t, map[string]string{"/search": searchAnswer, "/next": webMixAnswer})
	r := withYouTube(t, &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL})
	got := Exec(&fakeAPI{}, "play_youtube", `{"query":"Juan Luis Guerra","mode":"artist"}`)
	if !strings.HasPrefix(got, "Poniendo canciones de Juan Luis Guerra en orden aleatorio. Empieza ") {
		t.Fatalf("reply %q", got)
	}
	if len(r.played) != 1 || !strings.HasPrefix(r.played[0], "yt:song") {
		t.Fatalf("played %v", r.played)
	}
	cur, name, _ := svc.Player.Current()
	if name != "canciones de Juan Luis Guerra" || !strings.Contains(got, spokenTitle(cur)) {
		t.Fatalf("current %+v %q", cur, name)
	}
	want := "Está sonando " + spokenTitle(cur) + ", de " + cur.Artist + "."
	if np := Exec(&fakeAPI{}, "now_playing", ``); np != want {
		t.Fatalf("now_playing=%q want %q", np, want)
	}
}

func TestPlayYouTubeSongThenSimilar(t *testing.T) {
	srv, _ := fakeInnertube(t, map[string]string{"/search": searchAnswer, "/next": musicMixAnswer})
	r := withYouTube(t, &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL})
	got := Exec(&fakeAPI{}, "play_youtube", `{"query":"bachata rosa","mode":"song"}`)
	if got != "Poniendo Bachata Rosa, de Juan Luis Guerra, y después canciones parecidas." {
		t.Fatalf("reply %q", got)
	}
	if len(r.played) != 1 || r.played[0] != "yt:song1" {
		t.Fatalf("the requested song plays first: %v", r.played)
	}
	if got := Exec(&fakeAPI{}, "media_control", `{"action":"next"}`); !strings.Contains(got, "Provenza") {
		t.Fatalf("next must go to the similar songs: %q", got)
	}
}

func TestPlayYouTubeLikeCurrent(t *testing.T) {
	srv, _ := fakeInnertube(t, map[string]string{"/next": musicMixAnswer})
	r := withYouTube(t, &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL})
	if got := Exec(&fakeAPI{}, "play_youtube", `{"mode":"like_current"}`); got != "No está sonando nada de YouTube para buscar parecidas." {
		t.Fatalf("nothing playing: %q", got)
	}
	svc.Player.Start("x", []Track{{Title: "Provenza", Artist: "KAROL G", URL: "yt:seed"}}, false)
	got := Exec(&fakeAPI{}, "play_youtube", `{"mode":"like_current"}`)
	if got != "Poniendo canciones parecidas a Provenza. Empieza Según Quién, de Maluma." {
		t.Fatalf("reply %q", got)
	}
	if r.played[len(r.played)-1] != "yt:m2" {
		t.Fatalf("must skip the current song: %v", r.played)
	}
}

func TestPlayYouTubeFailures(t *testing.T) {
	old := *svc
	defer func() { *svc = old }()
	svc.YT = nil
	if got := Exec(&fakeAPI{}, "play_youtube", `{"query":"x","mode":"artist"}`); got != "Todavía no puedo poner música de YouTube." {
		t.Fatalf("no client: %q", got)
	}
	srv, _ := fakeInnertube(t, map[string]string{"/search": searchAnswer, "/next": webMixAnswer})
	withYouTube(t, &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL})
	if got := Exec(&fakeAPI{}, "play_youtube", `{"query":"Metallica","mode":"artist"}`); got != "No encontré canciones de Metallica en YouTube." {
		t.Fatalf("unknown artist: %q", got)
	}
	if got := Exec(&fakeAPI{}, "play_youtube", `{"query":"","mode":"song"}`); got != "¿Qué canción o artista quieres?" {
		t.Fatalf("empty query: %q", got)
	}
}

func TestSpokenTitle(t *testing.T) {
	cases := []struct{ title, artist, want string }{
		{"Juan Luis Guerra 4.40 - Vale la Pena (Live) (Video Oficial)", "Juan Luis Guerra", "Vale la Pena"},
		{"KAROL G, Shakira - TQG (Official Video)", "KAROL G", "TQG"},
		{"Bachata Rosa [Remastered 2005]", "Juan Luis Guerra", "Bachata Rosa"},
		{"Según Quién", "Maluma", "Según Quién"},
		{"(Live)", "X", "(Live)"},
	}
	for _, c := range cases {
		if got := spokenTitle(Track{Title: c.title, Artist: c.artist}); got != c.want {
			t.Errorf("spokenTitle(%q)=%q want %q", c.title, got, c.want)
		}
	}
}

const marcSearch = `{"contents":{"x":[
 {"videoRenderer":{"videoId":"marc1","title":{"runs":[{"text":"Marc Anthony - Vivir Mi Vida"}]},"ownerText":{"runs":[{"text":"Marc Anthony"}]},"lengthText":{"simpleText":"4:12"}}},
 {"videoRenderer":{"videoId":"other","title":{"runs":[{"text":"Otro"}]},"ownerText":{"runs":[{"text":"Romeo Santos"}]},"lengthText":{"simpleText":"3:30"}}}
]}}`

const marcMix = `{"contents":{"twoColumnWatchNextResults":{"playlist":{"playlist":{"contents":[
 {"playlistPanelVideoRenderer":{"videoId":"marc1","title":{"simpleText":"Vivir Mi Vida"},"shortBylineText":{"runs":[{"text":"Marc Anthony"}]},"lengthText":{"simpleText":"4:12"}}},
 {"playlistPanelVideoRenderer":{"videoId":"marc2","title":{"simpleText":"Valió la Pena"},"shortBylineText":{"runs":[{"text":"Marc Anthony"}]},"lengthText":{"simpleText":"4:50"}}}
]}}}}}`

// Speech recognition spells names as they sound ("Mark Anthony").
func TestYouTubeArtistToleratesMisspelledNames(t *testing.T) {
	srv, _ := fakeInnertube(t, map[string]string{"/search": marcSearch, "/next": marcMix})
	y := &YouTube{HTTP: srv.Client(), Web: srv.URL, Music: srv.URL}
	got, err := y.Artist("Mark Anthony")
	if err != nil || len(got) != 2 || got[0].Artist != "Marc Anthony" {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := y.Artist("Metallica"); err == nil {
		t.Fatal("an unrelated name must still fail")
	}
}

func TestSimilarName(t *testing.T) {
	cases := map[[2]string]bool{
		{"mark anthony", "marc anthony"}:              true,
		{"juan luis guerra", "juan luis guerra 4.40"}: true,
		{"carlos vives", "carlos bibes"}:              true,
		{"metallica", "marc anthony"}:                 false,
		{"fonseca", "romeo santos"}:                   false,
	}
	for c, want := range cases {
		if got := similarName(c[0], c[1]); got != want {
			t.Errorf("similarName(%q, %q)=%v want %v", c[0], c[1], got, want)
		}
	}
}
