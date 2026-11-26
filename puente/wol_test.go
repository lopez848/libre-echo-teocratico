package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Synthetic pages with the same markup as wol.jw.org (no copied content).
const wolWeekPage = `<html><body><div class="todayItems">
<h2>Vida y Ministerio</h2><a href="/es/wol/d/r4/lp-s/111" class="it"><p>1-7 de diciembre</p><p>Guía de actividad</p></a>
<h2>Estudio de La Atalaya</h2><a href="/es/wol/d/r4/lp-s/222" class="it"><p>Un artículo de prueba</p></a>
</div></body></html>`

const wolStudyPage = `<html><article id="article"><div class="scalableui">
<p class="contextTtl" data-pid="1">1-7 DE DICIEMBRE DE 2026</p>
<p class="pubRefs" data-pid="2">CANCIÓN 1 Una canción</p>
<h1 data-pid="3">Un artículo de prueba</h1>
<p class="themeScrp" data-pid="4">“Texto temático de prueba” (<a href="/es/wol/bc/x">Sal. 23:1</a>).</p>
<p class="pubRefs" data-pid="5">TEMA</p><p class="pubRefs" data-pid="6">Lo que vamos a aprender.</p>
<p class="qu" data-pid="40">1. ¿Primera pregunta?</p>
<p data-pid="7">ESTE es el primer párrafo.<a class="fn" href="/fn/1">*</a></p>
<p class="qu" data-pid="41">2. Según <a href="/es/wol/bc/x" class="b">Hebreos 4:16</a>, ¿segunda pregunta? (Mira también la imagen).</p>
<p data-pid="8">2 Segundo párrafo con una cita (<a href="/es/wol/bc/y" class="b">1 Juan 4:8</a>) y otra (Heb. 5:1).</p>
<figure><figcaption><p data-pid="78">Pie de una imagen que no se lee.</p></figcaption></figure>
<p data-pid="79">Otro pie de imagen sin número.</p>
<h2 data-pid="9">UN SUBTÍTULO</h2>
<p class="qu" data-pid="42">3, 4. ¿Pregunta doble?</p>
<p data-pid="10">3 Tercer párrafo.</p>
<p data-pid="11">4 Cuarto párrafo.</p>
<h2>¿QUÉ RESPONDERÍAS?</h2><ul><li><p>¿Repaso uno? Respuesta</p></li><li><p>¿Repaso dos? Respuesta</p></li></ul>
<p class="pubRefs" data-pid="37">CANCIÓN 2 Otra canción</p>
</div></article></html>`

const wolWorkbookPage = `<html><article id="article">
<h1>1-7 DE DICIEMBRE</h1>
<h2><a href="/es/wol/bc/z" class="b"><strong>LAMENTACIONES 3-5</strong></a></h2>
<h3>Canción 1 y oración | Palabras de introducción (1 min.)</h3>
<h2>TESOROS DE LA BIBLIA</h2>
<h3>1. Un discurso de prueba</h3>
<p>(10 mins.)</p><p>Un punto del discurso (Lam 3:22; w24.01 30 párr. 16).</p>
<h3>3. Lectura de la Biblia</h3><p>(4 mins.) Lam 3:19-42 (th lección 12).</p>
<h2>NUESTRA VIDA CRISTIANA</h2>
<h3>7. Una parte con preguntas</h3><p>(15 mins.) Análisis con el auditorio.</p>
<ul><li><p><a href="/es/wol/bc/q" class="b">Lam 3:27</a>. ¿Una pregunta del análisis? (w07 1/6 11 párr. 4). Respuesta</p></li></ul><p>Lee <a class="b" href="/bc">Mt 6:34</a>.</p>
<h3>Palabras de conclusión (3 mins.) | Canción 2 y oración</h3>
</article></html>`

func wolChapter(book, ch, verses int) string {
	var b strings.Builder
	b.WriteString(`<html><article id="article" class="article bible"><header><h1>Libro</h1></header>`)
	for v := 1; v <= verses; v++ {
		num := fmt.Sprintf(`<a class="vl vx vp" href="/x">%d </a>`, v)
		if v == 1 {
			num = fmt.Sprintf(`<a class="cl vx vp" href="/x"><strong>%d</strong> </a>`, ch)
		}
		fmt.Fprintf(&b, `<p class="sl"><span id="v%d-%d-%d-1" class="v">%sVersículo %d primera línea,<a class="fn pr" href="/fn">*</a></span></p>`, book, ch, v, num, v)
		fmt.Fprintf(&b, `<p class="sz"><span id="v%d-%d-%d-2" class="v">y segunda línea del versículo %d.<a class="b" href="/bc">+</a></span></p>`, book, ch, v, v)
	}
	b.WriteString(`</article></html>`)
	return b.String()
}

func TestParseStudyArticle(t *testing.T) {
	r, err := ParseStudyArticle(wolStudyPage)
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Un artículo de prueba" {
		t.Fatalf("title %q", r.Title)
	}
	var paras []int
	for _, p := range r.Pieces {
		paras = append(paras, p.Para)
	}
	if fmt.Sprint(paras) != "[0 1 2 3 4 0]" {
		t.Fatalf("paragraph numbers %v", paras)
	}
	head := r.Pieces[0].Text
	if !strings.Contains(head, "Un artículo de prueba") || !strings.Contains(head, "Texto temático de prueba") || !strings.Contains(head, "Salmo 23, versículo 1") {
		t.Fatalf("intro %q", head)
	}
	if p := r.Pieces[1].Text; p != "Párrafo 1. ¿Primera pregunta? Este es el primer párrafo." {
		t.Fatalf("paragraph 1 %q", p)
	}
	if p := r.Pieces[2].Text; !strings.HasPrefix(p, "Párrafo 2. Según Hebreos 4, versículo 16, ¿segunda pregunta?") || !strings.Contains(p, "(Primera de Juan 4, versículo 8)") || !strings.Contains(p, "Hebreos 5, versículo 1") || strings.Contains(p, "imagen") || strings.Contains(p, "Pie de") {
		t.Fatalf("paragraph 2 must drop captions and image notes: %q", p)
	}
	if p := r.Pieces[3].Text; p != "Un subtítulo. Párrafos 3 y 4. ¿Pregunta doble? Tercer párrafo." {
		t.Fatalf("paragraph 3 %q", p)
	}
	if p := r.Pieces[4].Text; p != "Cuarto párrafo." {
		t.Fatalf("paragraph 4 %q", p)
	}
	if p := r.Pieces[5].Text; p != "¿Qué responderías? ¿Repaso uno? ¿Repaso dos?" {
		t.Fatalf("review %q", p)
	}
}

func TestParseWorkbook(t *testing.T) {
	r, weekly, err := ParseWorkbook(wolWorkbookPage)
	if err != nil {
		t.Fatal(err)
	}
	if weekly != "LAMENTACIONES 3-5" || r.Title != "1-7 DE DICIEMBRE" {
		t.Fatalf("weekly=%q title=%q", weekly, r.Title)
	}
	all := ""
	for _, p := range r.Pieces {
		all += p.Text + "\n"
	}
	for _, want := range []string{"Lectura bíblica de la semana: Lamentaciones 3 al 5.", "Tesoros de la Biblia.", "1. Un discurso de prueba. 10 minutos. Un punto del discurso", "Lamentaciones 3, versículo 27. ¿Una pregunta del análisis?", "Lee Mateo 6, versículo 34."} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	for _, bad := range []string{"w24.01", "párr.", "th lección", "Respuesta", "(;", "()", "?.", "..", " ."} {
		if strings.Contains(all, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, all)
		}
	}
}

func TestParseWeeklyReading(t *testing.T) {
	cases := map[string]string{
		"LAMENTACIONES 3-5":        "25 3 5",
		"ISAÍAS 40, 41":            "23 40 41",
		"1 CORINTIOS 1-3":          "46 1 3",
		"SALMOS 119":               "19 119 119",
		"CANTAR DE LOS CANTARES 2": "22 2 2",
	}
	for in, want := range cases {
		b, from, to, ok := ParseWeeklyReading(in)
		if !ok || fmt.Sprint(b, from, to) != want {
			t.Errorf("%q -> %d %d %d %v, want %s", in, b, from, to, ok, want)
		}
	}
	if _, _, _, ok := ParseWeeklyReading("TESOROS DE LA BIBLIA"); ok {
		t.Error("a section title is not a Bible reading")
	}
}

func TestBibleBook(t *testing.T) {
	cases := map[string]int{"Juan": 43, "juan": 43, "primera de juan": 62, "1 Juan": 62, "Salmos": 19, "salmo": 19,
		"Isaías": 23, "isaias": 23, "Apocalipsis": 66, "Génesis": 1, "hechos": 44, "Cantares": 22, "Primera a los Corintios": 46}
	for in, want := range cases {
		if n, _, ok := BibleBook(in); !ok || n != want {
			t.Errorf("BibleBook(%q)=%d,%v want %d", in, n, ok, want)
		}
	}
	if _, _, ok := BibleBook("Mormón"); ok {
		t.Error("unknown book must fail")
	}
}

func TestParseBibleChapterVerses(t *testing.T) {
	page := wolChapter(23, 40, 12)
	r, err := ParseBibleChapter(page, 23, 40, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, p := range r.Pieces {
		all += p.Text + " "
	}
	if !strings.HasPrefix(all, "Isaías 40. Versículo 1 primera línea, y segunda línea del versículo 1.") {
		t.Fatalf("start %q", all[:90])
	}
	if !strings.Contains(all, "versículo 12.") || strings.Contains(all, "*") || strings.Contains(all, "+") || strings.Contains(all, "40 Versículo") {
		t.Fatalf("whole chapter, no markers: %q", all)
	}
	quoted := strings.Replace(page, `<span id="v23-40-3-1" class="v"><a class="vl vx vp" href="/x">3 </a>Versículo 3`, `<span id="v23-40-3-1" class="v"><a class="vl vx vp" href="/x">3 </a>”Versículo 3`, 1)
	if q, _ := ParseBibleChapter(quoted, 23, 40, 3, 3); !strings.Contains(q.Pieces[0].Text, "versículo 3. Versículo 3") {
		t.Fatalf("a passage must not start with the previous verse's closing quote: %q", q.Pieces[0].Text)
	}
	r, _ = ParseBibleChapter(page, 23, 40, 3, 4)
	got := ""
	for _, p := range r.Pieces {
		got += p.Text + " "
	}
	if strings.TrimSpace(got) != "Isaías 40, versículos 3 al 4. Versículo 3 primera línea, y segunda línea del versículo 3. Versículo 4 primera línea, y segunda línea del versículo 4." {
		t.Fatalf("range %q", got)
	}
	if _, err := ParseBibleChapter(page, 23, 40, 30, 31); err == nil {
		t.Fatal("verses outside the chapter must fail")
	}
}

func fakeWOL(t *testing.T) (*JW, *[]string) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case strings.HasPrefix(r.URL.Path, "/es/wol/meetings/r4/lp-s/"):
			w.Write([]byte(wolWeekPage))
		case r.URL.Path == "/es/wol/d/r4/lp-s/111":
			w.Write([]byte(wolWorkbookPage))
		case r.URL.Path == "/es/wol/d/r4/lp-s/222":
			w.Write([]byte(wolStudyPage))
		case strings.HasPrefix(r.URL.Path, "/es/wol/b/r4/lp-s/nwtsty/"):
			var b, c int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/es/wol/b/r4/lp-s/nwtsty/"), "%d/%d", &b, &c)
			n := 3
			if c == 23 {
				n = 12 // long enough to need the reader
			}
			w.Write([]byte(wolChapter(b, c, n)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	loc, _ := time.LoadLocation("America/Chicago")
	return &JW{HTTP: srv.Client(), WOLBase: srv.URL, Location: loc}, &paths
}

func TestWOLWeekPagesUseISOWeek(t *testing.T) {
	j, paths := fakeWOL(t)
	loc, _ := time.LoadLocation("America/Chicago")
	thu := time.Date(2026, 11, 26, 20, 0, 0, 0, loc)
	r, err := j.Watchtower(context.Background(), thu)
	if err != nil || r.Title != "Un artículo de prueba" {
		t.Fatalf("%+v %v", r.Title, err)
	}
	if (*paths)[0] != "/es/wol/meetings/r4/lp-s/2026/48" {
		t.Fatalf("this week: %v", *paths)
	}
	j.Watchtower(context.Background(), thu.AddDate(0, 0, 7))
	if (*paths)[2] != "/es/wol/meetings/r4/lp-s/2026/49" {
		t.Fatalf("next week: %v", *paths)
	}
	wr, err := j.WeeklyBibleReading(context.Background(), thu)
	if err != nil {
		t.Fatal(err)
	}
	last := (*paths)[len(*paths)-3:]
	if fmt.Sprint(last) != "[/es/wol/b/r4/lp-s/nwtsty/25/3 /es/wol/b/r4/lp-s/nwtsty/25/4 /es/wol/b/r4/lp-s/nwtsty/25/5]" {
		t.Fatalf("weekly chapters fetched: %v", *paths)
	}
	if !strings.HasPrefix(wr.Pieces[0].Text, "Lamentaciones 3.") || wr.Title != "Lamentaciones 3 al 5" {
		t.Fatalf("weekly reading %q %q", wr.Title, wr.Pieces[0].Text)
	}
}
