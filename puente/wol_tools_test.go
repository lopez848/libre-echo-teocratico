package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func withWOL(t *testing.T) (*fakeVoice, *[]string) {
	t.Helper()
	old := *svc
	t.Cleanup(func() { svc.Reader.Stop(); *svc = old })
	j, paths := fakeWOL(t)
	loc, _ := time.LoadLocation("America/Chicago")
	svc.JW, svc.Location = j, loc
	svc.Now = func() time.Time { return time.Date(2026, 11, 26, 20, 0, 0, 0, loc) }
	v := &fakeVoice{}
	svc.Reader = &Reader{Voice: v, Poll: 2 * time.Millisecond, Idle: 5 * time.Millisecond, StartWait: 5 * time.Millisecond}
	return v, paths
}

func spokenAll(v *fakeVoice) string { return strings.Join(v.said(), " ") }

func TestReadWatchtowerThisAndNextWeek(t *testing.T) {
	v, paths := withWOL(t)
	got := Exec(&fakeAPI{}, "read_watchtower", `{}`)
	if got != "Voy a leer el estudio de La Atalaya de esta semana: Un artículo de prueba." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return strings.Contains(spokenAll(v), Speakable("¿Repaso dos?")) })
	if !strings.HasPrefix(spokenAll(v), Speakable("El siguiente artículo se estudiará durante la semana del 1 al 7 de diciembre. Un artículo de prueba.")) {
		t.Fatalf("reader must start at the intro: %q", spokenAll(v))
	}
	if got := Exec(&fakeAPI{}, "read_watchtower", `{"week":"siguiente"}`); got != "Voy a leer el estudio de La Atalaya de la próxima semana: Un artículo de prueba." {
		t.Fatalf("next week reply %q", got)
	}
	if !strings.Contains(strings.Join(*paths, " "), "/es/wol/meetings/r4/lp-s/2026/49") {
		t.Fatalf("next week page not fetched: %v", *paths)
	}
}

func TestReadWorkbookAndWeeklyReading(t *testing.T) {
	v, _ := withWOL(t)
	if got := Exec(&fakeAPI{}, "read_workbook", `{}`); got != "Voy a leer la guía de actividades de esta semana." {
		t.Fatalf("workbook reply %q", got)
	}
	waitFor(t, func() bool { return strings.Contains(spokenAll(v), Speakable("Palabras de conclusión")) })
	svc.Reader.Stop()
	if got := Exec(&fakeAPI{}, "read_weekly_bible_reading", `{}`); got != "Voy a leer la lectura bíblica de esta semana: Lamentaciones 3 al 5." {
		t.Fatalf("weekly reply %q", got)
	}
	waitFor(t, func() bool { return strings.Contains(spokenAll(v), Speakable("Lamentaciones 5.")) })
}

func TestReadBible(t *testing.T) {
	v, _ := withWOL(t)
	// A short passage is answered directly; no reader.
	got := Exec(&fakeAPI{}, "read_bible", `{"book":"Juan","chapter":3,"verse_start":2}`)
	if got != "Juan 3, versículo 2. Versículo 2 primera línea, y segunda línea del versículo 2." || svc.Reader.Active() {
		t.Fatalf("short passage %q active=%v", got, svc.Reader.Active())
	}
	// A whole chapter is read by the reader.
	if got := Exec(&fakeAPI{}, "read_bible", `{"book":"salmos","chapter":23}`); got != "Voy a leer Salmo 23." {
		t.Fatalf("chapter reply %q", got)
	}
	waitFor(t, func() bool { return strings.Contains(spokenAll(v), Speakable("versículo 12.")) })
	if got := Exec(&fakeAPI{}, "read_bible", `{"book":"Mormón","chapter":1}`); got != "No encontré ese libro de la Biblia." {
		t.Fatalf("unknown book %q", got)
	}
	if got := Exec(&fakeAPI{}, "read_bible", `{"book":"Juan","chapter":3,"verse_start":40}`); got != "No pude traer ese texto de la Biblia." {
		t.Fatalf("missing verse %q", got)
	}
}

func TestResumeAndParagraph(t *testing.T) {
	v, _ := withWOL(t)
	if got := Exec(&fakeAPI{}, "resume_reading", `{}`); got != "No hay ninguna lectura para continuar." {
		t.Fatalf("nothing to resume %q", got)
	}
	// "lee el párrafo 3" with nothing loaded opens this week's study article.
	if got := Exec(&fakeAPI{}, "read_paragraph", `{"paragraph":3}`); got != "Leyendo el párrafo 3 de Un artículo de prueba." {
		t.Fatalf("paragraph reply %q", got)
	}
	waitFor(t, func() bool { return len(v.said()) > 0 })
	if first := v.said()[0]; !strings.HasPrefix(first, Speakable("Un subtítulo.")) {
		t.Fatalf("must start at paragraph 3: %q", first)
	}
	svc.Reader.Stop()
	if got := Exec(&fakeAPI{}, "resume_reading", `{}`); !strings.HasPrefix(got, "Sigo con Un artículo de prueba, desde el párrafo ") {
		t.Fatalf("resume reply %q", got)
	}
	if got := Exec(&fakeAPI{}, "read_paragraph", `{"paragraph":40}`); got != "Ese artículo no tiene párrafo 40." {
		t.Fatalf("bad paragraph %q", got)
	}
}

func TestNewTurnStopsTheReader(t *testing.T) {
	withWOL(t)
	svc.Reader.Start(Reading{Title: "x", Pieces: []ReadPiece{{Text: "Uno."}, {Text: "Dos."}}}, 0)
	hits := 0
	up := newUpstream(t, `{}`, &hits)
	defer up.Close()
	s := &server{upstream: up.URL, api: &fakeAPI{}, client: up.Client()}
	s.chat(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"para"}]}`)))
	if svc.Reader.Active() {
		t.Fatal("\"para\" must stop the reading")
	}
	if _, _, ok := svc.Reader.Resume(); ok {
		t.Fatal("\"para\" ends everything: nothing to resume (owner, 2026-11-26)")
	}
}

func TestReadingResumesAfterAnInterruptingTurn(t *testing.T) {
	v, _ := withWOL(t)
	long := Reading{Title: "Largo", Pieces: []ReadPiece{{Text: "Uno."}, {Text: "Dos."}, {Text: "Tres."}}}
	hits := 0
	up := newUpstream(t, `{"choices":[{"message":{"role":"assistant","content":"No entendí bien."}}]}`, &hits)
	defer up.Close()
	s := &server{upstream: up.URL, api: &fakeAPI{}, client: up.Client()}
	turn := func(text string) {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"messages":[{"role":"user","content":"`+text+`"}]}`))
		req.Header.Set("Authorization", "Bearer k")
		s.chat(httptest.NewRecorder(), req)
	}
	slow := &slowVoice{dur: 30 * time.Millisecond}
	svc.Reader.Voice = slow
	svc.Reader.Start(long, 0)
	waitFor(t, func() bool { return len(slow.said()) >= 1 })
	turn("qué hora es") // a false wake or a quick question
	if !svc.Reader.Active() {
		t.Fatal("after a turn that is not a stop, the reading must resume")
	}
	waitFor(t, func() bool { return strings.Contains(strings.Join(slow.said(), " "), "Tres.") })
	svc.Reader.Start(long, 0)
	turn("para")
	if svc.Reader.Active() {
		t.Fatal("a stop word must leave the reading stopped")
	}
	_ = v
}

func TestJWHelpListsEveryReadingAndHowToAsk(t *testing.T) {
	got := Exec(&fakeAPI{}, "jw_help", `{}`)
	for _, want := range []string{
		"texto diario", "estudio de La Atalaya", "próxima semana", "guía de actividades",
		"estudio de libro", "lectura bíblica de la semana", "Juan 3, versículo 16", "Salmo 23",
		"Música internacional", "«pausa»", "«continuar»", "«reanudar»", "«para» lo detiene todo", "lee el párrafo 5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help must mention %q: %s", want, got)
		}
	}
	for _, name := range []string{"read_watchtower", "read_workbook", "read_book_study", "read_weekly_bible_reading", "read_bible", "resume_reading", "read_paragraph", "daily_text", "play_jw_music", "jw_help"} {
		if !toolNames[name] {
			t.Errorf("tool %s must be offered to the model", name)
		}
	}
}

func TestJWHelpQuestionSkipsTheModel(t *testing.T) {
	hits := 0
	up := newUpstream(t, `{"choices":[{"message":{"role":"assistant","content":"resumen inventado"}}]}`, &hits)
	defer up.Close()
	s := &server{upstream: up.URL, api: &fakeAPI{}, client: up.Client()}
	for _, q := range []string{"¿Qué puedes hacer con jw.org?", "que puedes hacer con jota doble uve", "qué sabes hacer con jw punto org", "qué puedes hacer con la biblioteca en línea"} {
		got := s.answer(context.Background(), inbound{Messages: []chatMessage{{Role: "user", Content: q}}}, "Bearer k")
		if got != jwHelp {
			t.Errorf("%q -> %q", q, got)
		}
	}
	if hits != 0 {
		t.Fatalf("the help question must not go to the model: %d calls", hits)
	}
	if got := s.answer(context.Background(), inbound{Messages: []chatMessage{{Role: "user", Content: "qué puedes hacer"}}}, "Bearer k"); got == jwHelp {
		t.Fatal("a general question is not the jw.org help")
	}
}
