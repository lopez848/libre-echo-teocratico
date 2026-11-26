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

// Synthetic chapter with the markup of the "Seamos valientes" book on wol.
const bookChapterPage = `<html><article id="article">
<figure><img src="/x" alt="" /></figure>
<p class="contextTtl dc-bl" data-pid="1">18 GEDEÓN</p>
<h1 data-pid="2">Valiente pero prudente</h1>
<p data-pid="3">PARA ser valiente hay que ser prudente. Así era el juez.</p>
<p data-pid="4">Pablo lo menciona entre los hombres de fe (<a class="b" href="/bc">Heb. 11:32</a>).</p>
<figure><img src="/y" alt="Gedeón y sus hombres con antorchas." /><figcaption class="figcaption"><p data-pid="5">Jehová le dijo que redujera el ejército.</p></figcaption></figure>
<p data-pid="6">Gedeón los persiguió y los derrotó.</p>
<h3>Lea el relato bíblico</h3><ul><li><p>Jueces 6:1-7:22; 8:4-23, 28</p></li></ul>
<h3>¿Qué diría?</h3><p>¿De qué maneras demostró valor Gedeón?</p>
<h2>Investigue un poco más</h2><ul>
<li><p>1. ¿Por qué el pan es un símbolo adecuado? (Lea <a class="b" href="/bc">Jue 7:13</a> y vea <em>it</em> “Cebada” párr. 7). A Respuesta</p><figure><img src="/z" alt="Un cuerno de carnero." /><figcaption class="figcaption"><p>Imagen A: Los cuernos se usaban para dar señales.</p></figcaption></figure></li>
<li><p>2. ¿Por qué se asustaron? (<em>w04</em> 15/10 16 párrs. 9, 10). B Respuesta</p></li><li><p>3. ¿Qué símbolo era? (<a class="b" href="/bc">Juec. 7:13</a>; <em>it</em> “Cebada” párr. 7).</p></li></ul>
<h2>Piense en las lecciones</h2><ul><li><p>Gedeón fue humilde (<a class="b" href="/bc">Juec. 8:1-3</a>). ¿Cómo podemos imitarlo? Respuesta</p></li></ul>
<h2>Vea el cuadro completo</h2><ul><li><p>¿Qué me enseña este relato sobre Jehová? Respuesta</p></li></ul>
<h2>Para saber más</h2>
<p>Si le dan una responsabilidad, ¿cómo puede imitar a Gedeón?</p>
<p>“La sabiduría está con los modestos” (<em>w00</em> 1/8 16-18 párrs. 11-15)</p>
<p>“Jehová le da fuerzas a Gedeón” (<em>ijwis</em> artículo 13)</p>
</article></html>`

func TestParseBookChapter(t *testing.T) {
	r, err := ParseBookChapter(bookChapterPage)
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Capítulo 18. Gedeón. Valiente pero prudente" {
		t.Fatalf("title %q", r.Title)
	}
	var got []string
	for _, p := range r.Pieces {
		got = append(got, fmt.Sprintf("%v|%s", p.Pause, p.Text))
	}
	want := []string{
		"false|Capítulo 18. Gedeón. Valiente pero prudente.",
		"false|Para ser valiente hay que ser prudente. Así era el juez.",
		"false|Pablo lo menciona entre los hombres de fe.",
		"true|La imagen muestra: Gedeón y sus hombres con antorchas. El comentario dice: Jehová le dijo que redujera el ejército.",
		"false|Gedeón los persiguió y los derrotó.",
		"true|Lea el relato bíblico. Jueces 6, versículo 1, al 7, versículo 22; 8, versículos 4 al 23 y 28.",
		"true|¿Qué diría? ¿De qué maneras demostró valor Gedeón?",
		"true|Investigue un poco más. Punto 1. ¿Por qué el pan es un símbolo adecuado? Lea Jueces 7, versículo 13 y vea Perspicacia, entrada Cebada, párrafo 7.",
		"true|La imagen muestra un cuerno de carnero. El comentario dice: Los cuernos se usaban para dar señales.",
		"false|Punto 2. ¿Por qué se asustaron? Vea La Atalaya del 15 de octubre de 2004, página 16, párrafos 9 y 10. Punto 3. ¿Qué símbolo era? Lea Jueces 7, versículo 13 y vea Perspicacia, entrada Cebada, párrafo 7.",
		"true|Piense en las lecciones. Gedeón fue humilde. Lea Jueces 8, versículos 1 al 3. ¿Cómo podemos imitarlo?",
		"true|Vea el cuadro completo. ¿Qué me enseña este relato sobre Jehová?",
		"true|Para saber más. Si le dan una responsabilidad, ¿cómo puede imitar a Gedeón? Lea “La sabiduría está con los modestos”, en La Atalaya del 1 de agosto de 2000, páginas 16 a 18, párrafos 11 a 15. Lea “Jehová le da fuerzas a Gedeón”.",
		"true|Fin del capítulo 18.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("book pieces:\n%s", strings.Join(got, "\n"))
	}
}

func TestReadBookStudyFollowsTheWorkbookLink(t *testing.T) {
	old := *svc
	t.Cleanup(func() { svc.Reader.Stop(); *svc = old })
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case strings.HasPrefix(r.URL.Path, "/es/wol/meetings/"):
			w.Write([]byte(wolWeekPage))
		case r.URL.Path == "/es/wol/d/r4/lp-s/111":
			w.Write([]byte(wolWorkbookPage))
		case r.URL.Path == "/es/wol/pc/r4/lp-s/1/12/0":
			http.Redirect(w, r, "/es/wol/d/r4/lp-s/333", http.StatusFound)
		case r.URL.Path == "/es/wol/d/r4/lp-s/333":
			w.Write([]byte(bookChapterPage))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	loc, _ := time.LoadLocation("America/Chicago")
	svc.JW = &JW{HTTP: srv.Client(), WOLBase: srv.URL, Location: loc}
	svc.Location = loc
	svc.Now = func() time.Time { return time.Date(2026, 11, 26, 20, 0, 0, 0, loc) }
	v := &fakeVoice{}
	svc.Reader = &Reader{Voice: v, Poll: 2 * time.Millisecond, Idle: 5 * time.Millisecond, StartWait: 5 * time.Millisecond}
	got := Exec(&fakeAPI{}, "read_book_study", `{}`)
	if got != "Voy a leer el estudio bíblico de la congregación de esta semana: Capítulo 18. Gedeón. Valiente pero prudente." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return strings.Contains(spokenAll(v), Speakable("Fin del capítulo 18.")) })
	if !strings.Contains(strings.Join(paths, " "), "/es/wol/d/r4/lp-s/333") {
		t.Fatalf("chapter not fetched: %v", paths)
	}
	_ = context.Background
}
