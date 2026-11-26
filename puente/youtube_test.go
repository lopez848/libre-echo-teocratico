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
