package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func media(title, pub, url string) string {
	return `{"title":"` + title + `","firstPublished":"` + pub + `","duration":600,"files":[{"mimetype":"video/mp4","filesize":20000000,"progressiveDownloadURL":"` + url + `_720.mp4"},{"mimetype":"video/mp4","filesize":5000000,"progressiveDownloadURL":"` + url + `_240.mp4"}]}`
}

var broadcastingCategories = map[string]string{
	"StudioMonthlyPrograms": `{"category":{"key":"StudioMonthlyPrograms","type":"ondemand","media":[` +
		media("Programa de octubre", "2026-10-01T10:00:00Z", "https://v/oct") + `,` +
		media("Programa de noviembre", "2026-11-01T10:00:00Z", "https://v/nov") + `]}}`,
	// A container: its videos are in the subcategories.
	"VODIntExp": `{"category":{"key":"VODIntExp","type":"container","media":[],"subcategories":[
		{"key":"VODIntExpEndurance","type":"ondemand","media":[` + media("Aguantó con fe", "2026-09-01T10:00:00Z", "https://v/fe") + `]},
		{"key":"VODIntExpYouth","type":"ondemand","media":[` + media("Una joven valiente", "2026-11-10T10:00:00Z", "https://v/joven") + `]}]}}`,
	"VODProgramsEvents": `{"category":{"key":"VODProgramsEvents","type":"container","subcategories":[
		{"key":"VODPgmEvtMorningWorship","type":"ondemand","media":[]},
		{"key":"2025Convention","type":"ondemand","media":[` + media("Asamblea 2025, viernes", "2025-08-01T10:00:00Z", "https://v/a25") + `]},
		{"key":"2026Convention","type":"ondemand","media":[` + media("Asamblea 2026, sábado", "2026-08-02T10:00:00Z", "https://v/a26s") + `,` + media("Asamblea 2026, viernes", "2026-08-01T10:00:00Z", "https://v/a26v") + `]}]}}`,
}

func withBroadcasting(t *testing.T) *fakeRadio {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if b, ok := broadcastingCategories[key]; ok && strings.Contains(r.URL.Path, "/categories/S/") {
			w.Write([]byte(b))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	old := *svc
	t.Cleanup(func() { *svc = old })
	r := &fakeRadio{}
	svc.JW = &JW{HTTP: srv.Client(), Mediator: srv.URL}
	svc.Player = &Player{Radio: r, Poll: time.Hour}
	return r
}

func TestBroadcastingMonthlyProgramMostRecent(t *testing.T) {
	r := withBroadcasting(t)
	got := Exec(&fakeAPI{}, "play_jw_broadcasting", `{"section":"programa_mensual","mode":"reciente"}`)
	if got != "Poniendo lo más reciente de los programas mensuales de JW Broadcasting: Programa de noviembre." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return r.count() == 1 })
	if r.played[0] != "https://v/nov_240.mp4" {
		t.Fatalf("must play the newest, smallest MP4: %v", r.played)
	}
}

func TestBroadcastingContainerCollectsSubcategories(t *testing.T) {
	r := withBroadcasting(t)
	got := Exec(&fakeAPI{}, "play_jw_broadcasting", `{"section":"experiencias","mode":"reciente"}`)
	if got != "Poniendo lo más reciente de las entrevistas y experiencias: Una joven valiente." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return r.count() == 1 })
	if got := Exec(&fakeAPI{}, "play_jw_broadcasting", `{"section":"experiencias"}`); !strings.HasPrefix(got, "Poniendo las entrevistas y experiencias en orden aleatorio. Empieza ") {
		t.Fatalf("random reply %q", got)
	}
}

func TestBroadcastingLatestConvention(t *testing.T) {
	r := withBroadcasting(t)
	got := Exec(&fakeAPI{}, "play_jw_broadcasting", `{"section":"asamblea","mode":"reciente"}`)
	if got != "Poniendo lo más reciente de la asamblea regional más reciente: Asamblea 2026, sábado." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return r.count() == 1 })
	if r.played[0] != "https://v/a26s_240.mp4" {
		t.Fatalf("played %v", r.played)
	}
}

func TestBroadcastingUnknownSection(t *testing.T) {
	withBroadcasting(t)
	if got := Exec(&fakeAPI{}, "play_jw_broadcasting", `{"section":"deportes"}`); !strings.HasPrefix(got, "No conozco esa sección de JW Broadcasting.") {
		t.Fatalf("reply %q", got)
	}
}

func TestSpokenKeepsTheJWAcronym(t *testing.T) {
	if got := spoken("JW Broadcasting: Noviembre de 2026"); got != "JW Broadcasting: Noviembre de 2026" {
		t.Fatalf("got %q", got)
	}
	if got := spoken("LA VERDAD CAMBIA VIDAS"); got != "La verdad cambia vidas" {
		t.Fatalf("shouted titles are still softened: %q", got)
	}
}
