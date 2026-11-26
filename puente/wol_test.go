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
<p data-pid="7">ESTE es el primer párrafo, es “una frase citada” (<a class="b" href="/bc/1">1 Juan 2:1</a>). Y una alusión (<a class="b" href="/bc/2">Sal. 86:5</a>).<a class="fn" href="/fn/1">*</a></p>
<p class="qu" data-pid="41">2. Según <a href="/es/wol/bc/x" class="b">Hebreos 4:16</a>, ¿segunda pregunta? (Mira también la imagen).</p>
<p data-pid="8">2 Segundo párrafo con una cita (<a href="/es/wol/bc/y" class="b">1 Juan 4:8</a>) y otra (Heb. 5:1).</p>
<figure><img src="/x" alt="Un hombre cruza un puente." width="1" height="1" /><figcaption class="figcaption"><p data-pid="78">Pie de una imagen (mira el párrafo 2).</p></figcaption></figure>
<p data-pid="79">Otro pie de imagen sin número.</p>
<h2 data-pid="9">UN SUBTÍTULO</h2>
<p class="qu" data-pid="42">3, 4. ¿Pregunta doble?</p>
<p data-pid="10">3 Tercer párrafo (<strong>lee</strong> <a href="/es/wol/bc/r" class="b"><strong>Hebreos 7:25</strong></a>). Sigue el párrafo tres.</p>
<p data-pid="11">4 Cuarto párrafo <strong>(lee</strong> <a href="/es/wol/bc/s" class="b"><strong>Hebreos 4:15</strong></a> <strong>y la nota).</strong></p>
<h2>¿QUÉ RESPONDERÍAS?</h2><ul><li><p>¿Repaso uno? Respuesta</p></li><li><p>¿Repaso dos? Respuesta</p></li></ul>
<p class="pubRefs" data-pid="37">CANCIÓN 2 Otra canción</p>
</div></article></html>`

const wolWorkbookPage = `<html><article id="article">
<h1>1-7 DE DICIEMBRE</h1>
<h2><a href="/es/wol/bc/z" class="b"><strong>LAMENTACIONES 3-5</strong></a></h2>
<h3>Canción 1 y oración | Palabras de introducción (1 min.)</h3>
<h2>TESOROS DE LA BIBLIA</h2>
<h3>1. Un discurso de prueba</h3>
<p>(10 mins.)</p><p>Un punto del discurso (Lam 3:22; w24.01 30 párr. 16).</p><p>Otro punto (Lam 3:23; Mt 6:34).</p><p>Un tercer punto (Lam 3:24).</p>
<h3>3. Lectura de la Biblia</h3><p>(4 mins.) Lam 3:19-42 (th lección 12).</p>
<h2>NUESTRA VIDA CRISTIANA</h2>
<h3>6. Discurso</h3><p>(5 mins.) w25.12 26-30. Título: Un título de prueba (th lección 16).</p>
<h3>7. Una parte con preguntas</h3><p>(15 mins.) Análisis con el auditorio.</p>
<figure><img src="/x" alt="Imagen del video “Un video de prueba”. Una hermana ayuda a su mamá." width="1" height="1" /></figure>
<ul><li><p><a href="/es/wol/bc/q" class="b">Lam 3:27</a>. ¿Una pregunta del análisis? (w07 1/6 11 párr. 4). Respuesta</p></li></ul><p>Lee <a class="b" href="/bc">Mt 6:34</a>.</p>
<h3>8. Estudio bíblico de la congregación</h3><p>(30 mins.) <a href="/es/wol/pc/r4/lp-s/1/12/0"><em>wcg</em> cap. 18</a>.</p>
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
		fmt.Fprintf(&b, `<p class="sl"><span id="v%d-%d-%d-1" class="v">%sVersículo %d primera línea<a class="fn pr" href="/es/wol/fn/r4/lp-s/9/%d">*</a>,</span></p>`, book, ch, v, num, v, v)
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
	if fmt.Sprint(paras) != "[0 1 2 2 3 4 0]" {
		t.Fatalf("paragraph numbers %v", paras)
	}
	if head := r.Pieces[0].Text; head != "El siguiente artículo se estudiará durante la semana del 1 al 7 de diciembre. Un artículo de prueba. Texto temático: “Texto temático de prueba” (Salmo 23, versículo 1). Canción 1: Una canción. Tema: Lo que vamos a aprender." {
		t.Fatalf("intro %q", head)
	}
	if p := r.Pieces[1].Text; p != "Párrafo 1. Pregunta: ¿Primera pregunta? Este es el primer párrafo, es “una frase citada” (Primera de Juan 2, versículo 1). Y una alusión." {
		t.Fatalf("paragraph 1 %q", p)
	}
	if p := r.Pieces[2].Text; !strings.HasPrefix(p, "Párrafo 2. Pregunta: Según Hebreos 4, versículo 16, ¿segunda pregunta?") || !strings.HasSuffix(p, "Segundo párrafo con una cita y otra.") || strings.Contains(p, "imagen") || strings.Contains(p, "Pie de") {
		t.Fatalf("paragraph 2 must drop captions and image notes: %q", p)
	}
	if img := r.Pieces[3]; img.Para != 2 || !img.Pause || img.Text != "En la imagen para este párrafo se ve que un hombre cruza un puente. El comentario dice: Pie de una imagen." {
		t.Fatalf("image piece %+v", img)
	}
	r.Pieces = append(r.Pieces[:3], r.Pieces[4:]...) // drop the image to keep the indexes below
	if p := r.Pieces[3].Text; p != "Un subtítulo. Párrafos 3 y 4. Pregunta: ¿Pregunta doble? Tercer párrafo"+leeMark+". Sigue el párrafo tres." {
		t.Fatalf("paragraph 3 %q", p)
	}
	if got := fmt.Sprint(r.Pieces[3].Reads); got != "[{58 7 25 25 false}]" {
		t.Fatalf("the bold 'lee' text must be recorded: %s", got)
	}
	if len(r.Pieces[2].Reads) != 0 {
		t.Fatalf("plain citations are not 'lee' texts: %v", r.Pieces[2].Reads)
	}
	if got := fmt.Sprint(r.Pieces[4].Reads); got != "[{58 4 15 15 true}]" {
		t.Fatalf("'(lee' inside the bold text must be recorded too: %s", got)
	}
	if p := r.Pieces[4].Text; p != "Cuarto párrafo"+leeMark+"." {
		t.Fatalf("paragraph 4 %q", p)
	}
	if p := r.Pieces[5].Text; p != "¿Qué responderías? ¿Repaso uno? ¿Repaso dos? Canción 2: Otra canción. Fin del artículo." {
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
	var got []string
	for _, p := range r.Pieces {
		got = append(got, fmt.Sprintf("%v|%s", p.Pause, p.Text))
	}
	want := []string{
		"false|Semana del 1 al 7 de diciembre. Lectura semanal de la Biblia: Lamentaciones 3 al 5.",
		"false|Canción 1 y oración. Palabras de introducción. 1 minuto.",
		"false|Tesoros de la Biblia. Punto 1. Un discurso de prueba. 10 minutos. Un punto del discurso." + leeMark + " La Atalaya de enero de 2024, página 30, párrafo 16. Otro punto." + leeMark + leeMark + " Un tercer punto." + leeMark,
		"false|Punto 3. Lectura de la Biblia. 4 minutos. Lamentaciones 3, versículos 19 al 42. Analiza el folleto Maestros, lección 12.",
		"false|Nuestra vida cristiana. Punto 6. Discurso. 5 minutos. La Atalaya de diciembre de 2025, páginas 26 a 30. Título: Un título de prueba. Analiza el folleto Maestros, lección 16.",
		"false|Punto 7. Una parte con preguntas. 15 minutos. Análisis con el auditorio.",
		"true|Aquí vemos una imagen del video “Un video de prueba”. Una hermana ayuda a su mamá.",
		"false|¿Una pregunta del análisis?" + leeMark + " La Atalaya del 1 de junio de 2007, página 11, párrafo 4. Lee Mateo 6, versículo 34.",
		"false|Punto 8. Estudio bíblico de la congregación. 30 minutos. Seamos valientes, capítulo 18.",
		"false|Palabras de conclusión. 3 minutos. Canción 2 y oración.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("workbook pieces:\n%s", strings.Join(got, "\n"))
	}
	if fmt.Sprint(r.Pieces[2].Reads) != "[{25 3 22 22 false} {25 3 23 23 false} {40 6 34 34 false} {25 3 24 24 false}]" || fmt.Sprint(r.Pieces[7].Reads) != "[{25 3 27 27 false}]" {
		t.Fatalf("citations to read: %v / %v", r.Pieces[2].Reads, r.Pieces[7].Reads)
	}
}

func TestSpeakPubRef(t *testing.T) {
	cases := map[string]string{
		"w24.01 30 párr. 16":         "La Atalaya de enero de 2024, página 30, párrafo 16",
		"w21.08 13 párrs. 17-19":     "La Atalaya de agosto de 2021, página 13, párrafos 17 a 19",
		"w07 1/6 11 párr. 4":         "La Atalaya del 1 de junio de 2007, página 11, párrafo 4",
		"w25.12 26-30":               "La Atalaya de diciembre de 2025, páginas 26 a 30",
		"th lección 12":              "Analiza el folleto Maestros, lección 12",
		"lmd lección 5 punto 4":      "Analiza el folleto Una obra de amor, lección 5, punto 4",
		"wcg cap. 18":                "Seamos valientes, capítulo 18",
		"w00 1/8 16-18 párrs. 11-15": "La Atalaya del 1 de agosto de 2000, páginas 16 a 18, párrafos 11 a 15",
	}
	for in, want := range cases {
		if got, ok := speakPubRef(in); !ok || got != want {
			t.Errorf("speakPubRef(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := speakPubRef("Lam 3:22"); ok {
		t.Error("a Bible citation is not a publication")
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
		case strings.HasPrefix(r.URL.Path, "/es/wol/fn/r4/lp-s/9/"):
			w.Write([]byte(`<html><article id="article"><h1>Nota</h1><div class="footnoteResult"><div class="groupFootnote"><div class="fn-ref"><p data-pid="1">O “una nota de prueba”.</p></div></div></div></article></html>`))
		case strings.HasPrefix(r.URL.Path, "/es/wol/b/r4/lp-s/nwtsty/"), strings.HasPrefix(r.URL.Path, "/es/wol/b/r4/lp-s/nwt/"):
			var b, c int
			rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/es/wol/b/r4/lp-s/nwtsty/"), "/es/wol/b/r4/lp-s/nwt/")
			fmt.Sscanf(rest, "%d/%d", &b, &c)
			n := 3
			if c == 23 {
				n = 12 // long enough to need the reader
			}
			if b == 58 {
				n = 30 // Hebreos 7:25 for the "lee" text
			}
			if b == 25 && c == 3 {
				n = 30 // Lamentaciones 3:22 and 3:27 in the workbook
			}
			if b == 40 && c == 6 {
				n = 34 // Mateo 6:34 in the workbook
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
	// The "lee" text is read where the paragraph mentions it, as separate
	// pieces after a pause, then the paragraph goes on.
	var p3 []string
	for _, p := range r.Pieces {
		if p.Para == 3 || p.Para == 4 {
			p3 = append(p3, fmt.Sprintf("%v|%s", p.Pause, p.Text))
		}
	}
	want := []string{
		"false|Un subtítulo. Párrafos 3 y 4. Pregunta: ¿Pregunta doble? Tercer párrafo.",
		"true|Hebreos 7, versículo 25, dice: Versículo 25 primera línea, y segunda línea del versículo 25.",
		"true|Sigue el párrafo tres.",
		"false|Cuarto párrafo.",
		"true|Hebreos 4, versículo 15, y su nota dicen: Versículo 15 primera línea (o “una nota de prueba”), y segunda línea del versículo 15.",
	}
	if strings.Join(p3, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paragraph 3 pieces:\n%s", strings.Join(p3, "\n"))
	}
	if (*paths)[0] != "/es/wol/meetings/r4/lp-s/2026/48" {
		t.Fatalf("this week: %v", *paths)
	}
	j.Watchtower(context.Background(), thu.AddDate(0, 0, 7))
	if !strings.Contains(strings.Join(*paths, " "), "/es/wol/meetings/r4/lp-s/2026/49") {
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

func TestTrimCitationsKeepsOnlyQuotedSources(t *testing.T) {
	cases := map[string]string{
		// Words in quotes from the text: the citation says where they come from.
		`es “un ayudante que está junto al Padre” (1 Juan 2:1) y que`: `es “un ayudante que está junto al Padre” (1 Juan 2:1) y que`,
		`“No se inquieten” (Mat. 6:34; Fil. 4:6).`:                    `“No se inquieten” (Mat. 6:34; Fil. 4:6).`,
		// The quote may come earlier in the same sentence (paragraph 12).
		`es “un ayudante que está junto al Padre” y que intercede por nosotros cuando hacemos algo malo (1 Juan 2:1; lee Hebreos 7:25).`: "es “un ayudante que está junto al Padre” y que intercede por nosotros cuando hacemos algo malo (1 Juan 2:1)" + leeMark + ".",
		// A quote in an earlier sentence does not count.
		`Dijo “algo”. Luego habló de otra cosa (Sal. 86:5).`: `Dijo “algo”. Luego habló de otra cosa.`,
		// Only an allusion: the citation is not read at all.
		`escucha lo que Jesús le dice sobre nosotros (Sal. 86:5).`: `escucha lo que Jesús le dice sobre nosotros.`,
		`El mundo está controlado por Satanás (1 Juan 5:19). Él`:   `El mundo está controlado por Satanás. Él`,
		// "lee" texts always stay; the rest of the group follows the rule.
		`habla por nosotros (1 Juan 2:1; lee Hebreos 7:25).`: "habla por nosotros" + leeMark + ".",
		`con él (lee Hebreos 4:16 y la nota).`:               "con él" + leeMark + ".",
		// Picture notes go away with the citations next to them.
		`¿Qué nos enseña? (Hebreos 7:25; mira también la imagen)`: `¿Qué nos enseña?`,
		// A citation that is part of the sentence stays.
		`Según Hebreos 4:16, ¿qué otra cosa es posible?`: `Según Hebreos 4:16, ¿qué otra cosa es posible?`,
		// A parenthesis that is not a citation stays.
		`con humildad (o sea, sin orgullo) y`: `con humildad (o sea, sin orgullo) y`,
	}
	for in, want := range cases {
		if got := trimCitations(in); got != want {
			t.Errorf("trimCitations(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestSpokenUppercaseAbbreviationInParens(t *testing.T) {
	if got := spoken("“Texto temático” (HEB. 5:1)."); got != "“Texto temático” (Hebreos 5, versículo 1)." {
		t.Fatalf("got %q", got)
	}
}

func TestWorkbookReadsCitedTexts(t *testing.T) {
	j, _ := fakeWOL(t)
	loc, _ := time.LoadLocation("America/Chicago")
	r, _, err := j.Workbook(context.Background(), time.Date(2026, 11, 26, 20, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, p := range r.Pieces {
		all += p.Text + "\n"
	}
	for _, want := range []string{
		"Un punto del discurso.\nLamentaciones 3, versículo 22, dice: Versículo 22 primera línea, y segunda línea del versículo 22.\nLa Atalaya de enero de 2024, página 30, párrafo 16. Otro punto.\nLamentaciones 3, versículo 23, dice: Versículo 23 primera línea, y segunda línea del versículo 23.\nMateo 6, versículo 34, dice: Versículo 34 primera línea, y segunda línea del versículo 34.\nUn tercer punto.\nLamentaciones 3, versículo 24, dice:",
		"¿Una pregunta del análisis?\nLamentaciones 3, versículo 27, dice: Versículo 27 primera línea, y segunda línea del versículo 27.\nLa Atalaya del 1 de junio de 2007",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, leeMark) {
		t.Errorf("marks must not be spoken:\n%s", all)
	}
}

func TestWorkbookLineCasingAndColon(t *testing.T) {
	if got, _ := workbookLine("(3 mins.) DE CASA EN CASA. Enseña una verdad bíblica."); got != "3 minutos. De casa en casa. Enseña una verdad bíblica." {
		t.Errorf("casing after a period: %q", got)
	}
	if got, _ := workbookLine("Pon el VIDEO de prueba. Luego pregunta:"); got != "Pon el video de prueba. Luego pregunta:" {
		t.Errorf("colon end: %q", got)
	}
}
