package main

import (
	"bytes"
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

const worshipCategory = `{"category":{"name":"Adoración matutina","media":[
 {"title":"Hermano Uno: Un tema (Sant. 3:2)","firstPublished":"2026-11-03T21:28:46.782Z","duration":604,"files":[{"label":"240p","mimetype":"video/mp4","filesize":11000000,"progressiveDownloadURL":"https://v/uno_240.mp4"},{"label":"720p","mimetype":"video/mp4","filesize":90000000,"progressiveDownloadURL":"https://v/uno_720.mp4"}]},
 {"title":"Hermano Dos: El más nuevo (Dan. 2:31-33)","firstPublished":"2026-11-20T13:42:19.057Z","duration":583,"files":[{"label":"144p","mimetype":"video/3gpp","filesize":9000000,"progressiveDownloadURL":"https://v/dos.3gp"},{"label":"240p","mimetype":"video/mp4","filesize":12000000,"progressiveDownloadURL":"https://v/dos_240.mp4"}]},
 {"title":"Hermano Tres: Otro tema","firstPublished":"2026-10-01T10:00:00Z","duration":600,"files":[{"label":"240p","mimetype":"video/mp4","filesize":10000000,"progressiveDownloadURL":"https://v/tres_240.mp4"}]}
]}}`

func withWorship(t *testing.T) *fakeRadio {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/categories/S/VODPgmEvtMorningWorship") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(worshipCategory))
	}))
	t.Cleanup(srv.Close)
	old := *svc
	t.Cleanup(func() { *svc = old })
	r := &fakeRadio{}
	svc.JW = &JW{HTTP: srv.Client(), Mediator: srv.URL}
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	return r
}

func TestMorningWorshipMostRecent(t *testing.T) {
	r := withWorship(t)
	got := Exec(&fakeAPI{}, "play_morning_worship", `{"mode":"reciente"}`)
	if got != "Poniendo la adoración matutina más reciente: Hermano Dos: El más nuevo (Daniel 2, versículos 31 al 33)." {
		t.Fatalf("reply %q", got)
	}
	if len(r.played) != 1 || r.played[0] != "https://v/dos_240.mp4" {
		t.Fatalf("must play the newest, smallest MP4: %v", r.played)
	}
	if np := Exec(&fakeAPI{}, "now_playing", ``); np != "Está sonando Hermano Dos: El más nuevo (Daniel 2, versículos 31 al 33), de la adoración matutina más reciente." {
		t.Fatalf("now_playing must read the citation: %q", np)
	}
}

func TestMorningWorshipRandom(t *testing.T) {
	r := withWorship(t)
	got := Exec(&fakeAPI{}, "play_morning_worship", `{}`)
	if !strings.HasPrefix(got, "Poniendo adoraciones matutinas en orden aleatorio. Empieza ") {
		t.Fatalf("reply %q", got)
	}
	if len(r.played) != 1 || !strings.HasSuffix(r.played[0], "_240.mp4") {
		t.Fatalf("played %v", r.played)
	}
	if _, name, ok := svc.Player.Current(); !ok || name != "adoraciones matutinas" {
		t.Fatalf("queue name %q", name)
	}
}

func TestBusPlayerStreamsMP4FromAURL(t *testing.T) {
	file := m4aTone(t, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.mp4", time.Time{}, bytes.NewReader(file))
	}))
	defer srv.Close()
	bus := filepath.Join(t.TempDir(), "media.pcm")
	os.WriteFile(bus, nil, 0o644)
	b := &BusPlayer{Bus: bus, HTTP: srv.Client()}
	if err := b.stream(context.Background(), srv.URL+"/video.mp4"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(bus)
	if frames, _ := pcmStats(out); frames < 47000 || frames > 51000 {
		t.Fatalf("frames=%d, want about one second at 48 kHz", frames)
	}
}

// Every jw.org voice tool must be in the "qué puedes hacer con jw.org"
// list, so new features are announced as they are added.
func TestJWHelpCoversEveryJWTool(t *testing.T) {
	listed := map[string]bool{}
	for _, f := range jwFeatures {
		for _, tool := range f.Tools {
			listed[tool] = true
		}
		if !strings.Contains(jwHelp, f.Say) {
			t.Errorf("help text misses %q", f.Say)
		}
	}
	for name := range toolNames {
		jw := strings.HasPrefix(name, "read_") || strings.HasPrefix(name, "play_jw") || name == "daily_text" ||
			name == "play_morning_worship" || name == "resume_reading"
		if jw && !listed[name] {
			t.Errorf("tool %s is not in the jw.org help list", name)
		}
	}
	if !strings.Contains(jwHelp, "adoración matutina") {
		t.Error(fmt.Sprintf("help must mention morning worship: %s", jwHelp))
	}
}

// Music must wait for agentd to finish "Poniendo …" instead of playing over it.
func TestMusicStartsAfterTheSpokenIntro(t *testing.T) {
	r := withWorship(t)
	v := &fakeVoice{busyUntil: time.Now().Add(80 * time.Millisecond)} // agentd still talking
	svc.Reader = &Reader{Voice: v, Poll: 2 * time.Millisecond, Idle: 20 * time.Millisecond, StartWait: time.Second}
	silent := time.Now().Add(80 * time.Millisecond)
	got := Exec(&fakeAPI{}, "play_morning_worship", `{"mode":"reciente"}`)
	if !strings.HasPrefix(got, "Poniendo la adoración matutina más reciente: ") {
		t.Fatalf("reply %q", got)
	}
	if r.count() != 0 {
		t.Fatal("the music must not start while the intro is spoken")
	}
	waitFor(t, func() bool { return r.count() == 1 })
	if time.Now().Before(silent) {
		t.Fatal("started before the intro ended")
	}
}
