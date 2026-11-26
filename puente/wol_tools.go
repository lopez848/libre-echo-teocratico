package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// wolWeek returns a time inside this week or the next one.
func wolWeek(week string) (time.Time, string) {
	t := svc.today()
	switch fold(strings.TrimSpace(week)) {
	case "siguiente", "proxima", "la proxima", "la siguiente", "next":
		return t.AddDate(0, 0, 7), "de la próxima semana"
	}
	return t, "de esta semana"
}

func wolCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// startReading hands a reading to the resumable reader after a short
// intro. With direct, a reading short enough for agentd is answered whole.
func startReading(r Reading, intro string, direct bool) string {
	full := ""
	for _, p := range r.Pieces {
		full = strings.TrimSpace(full + " " + p.Text)
	}
	if svc.Reader == nil || (direct && len(Speakable(full)) <= agentdReplyMax) {
		return full
	}
	svc.Reader.Start(r, 0)
	return intro
}

func readWatchtower(week string) string {
	if svc.JW == nil {
		return "Todavía no puedo leer La Atalaya."
	}
	t, label := wolWeek(week)
	ctx, cancel := wolCtx()
	defer cancel()
	r, err := svc.JW.Watchtower(ctx, t)
	if err != nil {
		logf("wol watchtower: %v", err)
		return "No pude traer el estudio de La Atalaya " + label + "."
	}
	return startReading(r, fmt.Sprintf("Voy a leer el estudio de La Atalaya %s: %s.", label, r.Title), false)
}

func readWorkbook(week string) string {
	if svc.JW == nil {
		return "Todavía no puedo leer la guía de actividades."
	}
	t, label := wolWeek(week)
	ctx, cancel := wolCtx()
	defer cancel()
	r, _, err := svc.JW.Workbook(ctx, t)
	if err != nil {
		logf("wol workbook: %v", err)
		return "No pude traer la guía de actividades " + label + "."
	}
	return startReading(r, "Voy a leer la guía de actividades "+label+".", false)
}

func readWeeklyBible(week string) string {
	if svc.JW == nil {
		return "Todavía no puedo leer la Biblia."
	}
	t, label := wolWeek(week)
	ctx, cancel := wolCtx()
	defer cancel()
	r, err := svc.JW.WeeklyBibleReading(ctx, t)
	if err != nil {
		logf("wol weekly reading: %v", err)
		return "No pude traer la lectura bíblica " + label + "."
	}
	return startReading(r, fmt.Sprintf("Voy a leer la lectura bíblica %s: %s.", label, r.Title), false)
}

func readBible(bookName string, chapter, from, to int) string {
	if svc.JW == nil {
		return "Todavía no puedo leer la Biblia."
	}
	book, _, ok := BibleBook(bookName)
	if !ok {
		return "No encontré ese libro de la Biblia."
	}
	if chapter <= 0 {
		return "¿Qué capítulo quieres?"
	}
	if from > 0 && to < from {
		to = from
	}
	ctx, cancel := wolCtx()
	defer cancel()
	r, err := svc.JW.Bible(ctx, book, chapter, from, to)
	if err != nil {
		logf("wol bible: %v", err)
		return "No pude traer ese texto de la Biblia."
	}
	return startReading(r, "Voy a leer "+r.Title+".", true)
}

func resumeReading() string {
	if svc.Reader == nil {
		return "No hay ninguna lectura para continuar."
	}
	title, para, ok := svc.Reader.Resume()
	if !ok {
		return "No hay ninguna lectura para continuar."
	}
	if para == 0 {
		return "Sigo con " + title + "."
	}
	return fmt.Sprintf("Sigo con %s, desde el párrafo %d.", title, para)
}

func readParagraph(n int) string {
	if svc.Reader == nil || n <= 0 {
		return "¿Qué párrafo quieres?"
	}
	title, ok := svc.Reader.Jump(n)
	if !ok && !svc.Reader.Loaded() && svc.JW != nil {
		// Nothing loaded yet: open this week's study article.
		ctx, cancel := wolCtx()
		r, err := svc.JW.Watchtower(ctx, svc.today())
		cancel()
		if err != nil {
			logf("wol watchtower: %v", err)
			return "No pude traer el estudio de La Atalaya de esta semana."
		}
		svc.Reader.Start(r, len(r.Pieces))
		svc.Reader.Stop()
		title, ok = svc.Reader.Jump(n)
	}
	if !ok {
		return fmt.Sprintf("Ese artículo no tiene párrafo %d.", n)
	}
	return fmt.Sprintf("Leyendo el párrafo %d de %s.", n, title)
}

func readBookStudy(week string) string {
	if svc.JW == nil {
		return "Todavía no puedo leer el estudio de libro."
	}
	t, label := wolWeek(week)
	ctx, cancel := wolCtx()
	defer cancel()
	r, err := svc.JW.BookStudy(ctx, t)
	if err != nil {
		logf("wol book study: %v", err)
		return "No pude traer el estudio bíblico de la congregación " + label + "."
	}
	return startReading(r, fmt.Sprintf("Voy a leer el estudio bíblico de la congregación %s: %s.", label, r.Title), false)
}

// jwFeatures lists every jw.org voice feature and how to ask for it. The
// help answer is built from it, and a test fails when a new jw.org tool is
// added without an entry here.
var jwFeatures = []struct {
	Tools []string
	Say   string
}{
	{[]string{"daily_text"}, "Leer el texto diario de hoy, de mañana o de ayer: di «lee el texto diario»."},
	{[]string{"read_watchtower"}, "Leer el estudio de La Atalaya de esta semana o de la próxima semana: «lee el estudio de La Atalaya»."},
	{[]string{"read_workbook"}, "Leer la guía de actividades de la reunión: «lee la guía de actividades»."},
	{[]string{"read_book_study"}, "Leer el estudio de libro de la congregación: «lee el estudio de libro»."},
	{[]string{"read_weekly_bible_reading"}, "Leer la lectura bíblica de la semana: «lee la lectura bíblica de esta semana»."},
	{[]string{"read_bible"}, "Leer cualquier capítulo o versículo de la Biblia: «lee Juan 3, versículo 16» o «lee el Salmo 23»."},
	{[]string{"play_jw_music"}, "Poner música de jw.org: «pon Música internacional» o «pon Canciones originales»."},
	{[]string{"play_morning_worship"}, "Poner la adoración matutina más reciente: «pon la adoración matutina más reciente», o varias en orden aleatorio: «pon adoraciones matutinas»."},
	{[]string{"resume_reading", "read_paragraph"}, "Mientras leo, puedes decir «para», después «continúa la lectura», o «lee el párrafo 5» para saltar a un párrafo."},
}

// jwHelp tells the owner everything Mia can do with jw.org and how to ask.
var jwHelp = func() string {
	parts := []string{"Con jw.org puedo hacer esto."}
	for _, f := range jwFeatures {
		parts = append(parts, f.Say)
	}
	return strings.Join(parts, " ")
}()

// isJWHelpQuestion recognizes "¿qué puedes hacer con jw.org?" however the
// speech recognizer spells jw.org, so the full list never depends on the
// model choosing the right tool.
func isJWHelpQuestion(text string) bool {
	f := fold(text)
	asks := false
	for _, a := range []string{"que puedes hacer", "que sabes hacer", "que mas puedes hacer", "que cosas puedes hacer", "que opciones"} {
		if strings.Contains(f, a) {
			asks = true
		}
	}
	if !asks {
		return false
	}
	for _, k := range []string{"jw", "jota", "doble uve", "punto org", "biblioteca en linea", "wol"} {
		if strings.Contains(f, k) {
			return true
		}
	}
	return false
}

func playMorningWorship(mode string) string {
	if svc.JW == nil || svc.Player == nil {
		return "Todavía no puedo poner la adoración matutina."
	}
	ctx, cancel := wolCtx()
	defer cancel()
	tracks, err := svc.JW.MorningWorship(ctx)
	if err != nil || len(tracks) == 0 {
		logf("jw morning worship: %v", err)
		return "No pude traer las adoraciones matutinas de jw.org."
	}
	if f := fold(mode); strings.Contains(f, "reciente") || strings.Contains(f, "ultima") || strings.Contains(f, "nueva") {
		first, err := svc.Player.Start("la adoración matutina más reciente", tracks[:1], false)
		if err != nil {
			return failed(err)
		}
		return "Poniendo la adoración matutina más reciente: " + spoken(first.Title) + "."
	}
	first, err := svc.Player.Start("adoraciones matutinas", tracks, true)
	if err != nil {
		return failed(err)
	}
	return "Poniendo adoraciones matutinas en orden aleatorio. Empieza " + spoken(first.Title) + "."
}
