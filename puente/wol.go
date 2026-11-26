package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ReadPiece is one spoken unit of a long reading. Para is the article
// paragraph or first Bible verse it starts at, 0 for intros and reviews.
type ReadPiece struct {
	Para int
	Text string
}

// Reading is a long text from wol.jw.org split into spoken pieces.
type Reading struct {
	Title  string
	Pieces []ReadPiece
}

// bibleOrder lists the 66 books in order: spoken name and folded aliases
// (numbered books use a "1 ", "2 " or "3 " prefix).
var bibleOrder = []struct {
	Name    string
	Aliases []string
}{
	{"Génesis", []string{"genesis"}}, {"Éxodo", []string{"exodo"}}, {"Levítico", []string{"levitico"}},
	{"Números", []string{"numeros"}}, {"Deuteronomio", []string{"deuteronomio"}}, {"Josué", []string{"josue"}},
	{"Jueces", []string{"jueces"}}, {"Rut", []string{"rut"}}, {"Primero de Samuel", []string{"1 samuel"}},
	{"Segundo de Samuel", []string{"2 samuel"}}, {"Primero de Reyes", []string{"1 reyes"}}, {"Segundo de Reyes", []string{"2 reyes"}},
	{"Primero de Crónicas", []string{"1 cronicas"}}, {"Segundo de Crónicas", []string{"2 cronicas"}}, {"Esdras", []string{"esdras"}},
	{"Nehemías", []string{"nehemias"}}, {"Ester", []string{"ester"}}, {"Job", []string{"job"}},
	{"Salmo", []string{"salmos", "salmo"}}, {"Proverbios", []string{"proverbios"}}, {"Eclesiastés", []string{"eclesiastes"}},
	{"El Cantar de los Cantares", []string{"cantar de los cantares", "el cantar de los cantares", "cantares", "cantar"}},
	{"Isaías", []string{"isaias"}}, {"Jeremías", []string{"jeremias"}}, {"Lamentaciones", []string{"lamentaciones"}},
	{"Ezequiel", []string{"ezequiel"}}, {"Daniel", []string{"daniel"}}, {"Oseas", []string{"oseas"}}, {"Joel", []string{"joel"}},
	{"Amós", []string{"amos"}}, {"Abdías", []string{"abdias"}}, {"Jonás", []string{"jonas"}}, {"Miqueas", []string{"miqueas"}},
	{"Nahúm", []string{"nahum"}}, {"Habacuc", []string{"habacuc"}}, {"Sofonías", []string{"sofonias"}}, {"Ageo", []string{"ageo"}},
	{"Zacarías", []string{"zacarias"}}, {"Malaquías", []string{"malaquias"}}, {"Mateo", []string{"mateo"}},
	{"Marcos", []string{"marcos"}}, {"Lucas", []string{"lucas"}}, {"Juan", []string{"juan"}},
	{"Hechos", []string{"hechos", "hechos de los apostoles"}}, {"Romanos", []string{"romanos"}},
	{"Primera a los Corintios", []string{"1 corintios"}}, {"Segunda a los Corintios", []string{"2 corintios"}},
	{"Gálatas", []string{"galatas"}}, {"Efesios", []string{"efesios"}}, {"Filipenses", []string{"filipenses"}},
	{"Colosenses", []string{"colosenses"}}, {"Primera a los Tesalonicenses", []string{"1 tesalonicenses"}},
	{"Segunda a los Tesalonicenses", []string{"2 tesalonicenses"}}, {"Primera a Timoteo", []string{"1 timoteo"}},
	{"Segunda a Timoteo", []string{"2 timoteo"}}, {"Tito", []string{"tito"}}, {"Filemón", []string{"filemon"}},
	{"Hebreos", []string{"hebreos"}}, {"Santiago", []string{"santiago"}}, {"Primera de Pedro", []string{"1 pedro"}},
	{"Segunda de Pedro", []string{"2 pedro"}}, {"Primera de Juan", []string{"1 juan"}}, {"Segunda de Juan", []string{"2 juan"}},
	{"Tercera de Juan", []string{"3 juan"}}, {"Judas", []string{"judas"}}, {"Apocalipsis", []string{"apocalipsis", "revelacion"}},
}

var reOrdinal = []struct {
	re  *regexp.Regexp
	num string
}{
	{regexp.MustCompile(`^(primero|primera|primer|1a|1ra|1o)( de| a los| a)? `), "1 "},
	{regexp.MustCompile(`^(segundo|segunda|2a|2da|2o)( de| a los| a)? `), "2 "},
	{regexp.MustCompile(`^(tercero|tercera|3a|3ra|3o)( de| a los| a)? `), "3 "},
}

// BibleBook resolves a spoken or written book name to its number (1-66).
func BibleBook(name string) (int, string, bool) {
	f := strings.TrimSpace(fold(name))
	f = strings.TrimPrefix(f, "el libro de ")
	f = strings.TrimPrefix(f, "libro de ")
	for _, o := range reOrdinal {
		f = o.re.ReplaceAllString(f, o.num)
	}
	if f == "" {
		return 0, "", false
	}
	for i, b := range bibleOrder {
		for _, a := range b.Aliases {
			if f == a {
				return i + 1, b.Name, true
			}
		}
	}
	// Prefix match for partial names ("lamentacion", "apocalip").
	for i, b := range bibleOrder {
		for _, a := range b.Aliases {
			if len(f) >= 4 && strings.HasPrefix(a, f) {
				return i + 1, b.Name, true
			}
		}
	}
	return 0, "", false
}

var reWeekly = regexp.MustCompile(`^(.+?)\s+(\d+)(?:\s*[-,–]\s*(\d+))?$`)

// ParseWeeklyReading reads a workbook heading like "LAMENTACIONES 3-5".
func ParseWeeklyReading(s string) (book, from, to int, ok bool) {
	m := reWeekly.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, 0, false
	}
	book, _, ok = BibleBook(m[1])
	if !ok {
		return 0, 0, 0, false
	}
	from, _ = strconv.Atoi(m[2])
	to = from
	if m[3] != "" {
		to, _ = strconv.Atoi(m[3])
	}
	return book, from, to, to >= from
}

func chapterRange(book, from, to int) string {
	name := bibleOrder[book-1].Name
	if from == to {
		return fmt.Sprintf("%s %d", name, from)
	}
	return fmt.Sprintf("%s %d al %d", name, from, to)
}

var (
	reElem  = regexp.MustCompile(`(?s)<(figure|h1|h2|h3|p|li)\b([^>]*)>(.*?)</(?:figure|h1|h2|h3|p|li)>`)
	reClass = regexp.MustCompile(`class="([^"]*)"`)
	// Footnote marks and the Bible's "+" cross-reference links; scripture
	// links in articles share class "b" but keep their text.
	reMarkers   = regexp.MustCompile(`(?s)<a[^>]*class="fn(?: [^"]*)?"[^>]*>.*?</a>|<a[^>]*class="b(?: [^"]*)?"[^>]*>\s*\+\s*</a>`)
	reWritten   = regexp.MustCompile(`((?:[123] )?[A-ZÁÉÍÓÚÑ][a-záéíóúñ]+(?: de los [A-Z][a-z]+)?) (\d+):(\d+)(?:-(\d+))?((?:, ?\d+)*)`)
	reDoubleDot = regexp.MustCompile(`([.?!])\s*\.`)
	reVerseNum  = regexp.MustCompile(`(?s)<a[^>]*class="(?:cl|vl)[^"]*"[^>]*>.*?</a>`)
	reLeadNums  = regexp.MustCompile(`^(\d+(?:\s*[,y-]\s*\d+)*)\.\s*`)
	reParaNum   = regexp.MustCompile(`^(\d+)\s+`)
	reImageNote = regexp.MustCompile(`\s*\((?:Mira|Vea|Ve|Vean) también [^)]*\)\.?`)
	reWbPubRef  = regexp.MustCompile(`\b(?:w|g|wp|km|mwb)\d{2}(?:\.\d{2})?(?: \d+/\d+)? \d+(?:-\d+)?(?: párrs?\. \d+(?:-\d+)?)?|\b(?:th|lmd|lff) lección \d+(?: punto \d+)?|\b(?:wcg|lfb|bhs|jy) (?:cap|lec)\. \d+`)
	reEmptyRef  = regexp.MustCompile(`\(\s*[;,]?\s*\)|;\s*\)|\(\s*;\s*`)
	reMins      = regexp.MustCompile(`\((\d+) mins?\.\)`)
)

func elemClass(attrs string) string {
	if m := reClass.FindStringSubmatch(attrs); m != nil {
		return m[1]
	}
	return ""
}

func cleanHTML(s string) string {
	return stripHTML(reMarkers.ReplaceAllString(s, ""))
}

var properNouns = map[string]string{"biblia": "Biblia", "jehova": "Jehová", "jesus": "Jesús", "dios": "Dios", "cristo": "Cristo", "israel": "Israel"}

// unshout lowercases words written in capitals (wol prints headings and the
// first word of an article that way) so the voice does not read them oddly.
func unshout(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		letters, upper := 0, 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters < 2 || upper != letters {
			continue
		}
		lw := strings.ToLower(w)
		if _, ok := bibleBooks[capitalize(lw)]; ok { // "HEB." -> "Heb."
			words[i] = capitalize(lw)
			continue
		}
		if p, ok := properNouns[fold(strings.Trim(lw, ".,;:¿?¡!"))]; ok {
			lw = strings.Replace(lw, strings.ToLower(p), p, 1)
		}
		if i == 0 {
			lw = capitalize(lw)
		}
		words[i] = lw
	}
	return strings.Join(words, " ")
}

func capitalize(s string) string {
	r := []rune(s)
	for i, c := range r {
		if unicode.IsLetter(c) {
			r[i] = unicode.ToUpper(c)
			break
		}
	}
	return string(r)
}

// wbAbbrev are the dot-less book abbreviations of the meeting workbook.
var wbAbbrev = map[string]int{"gen": 1, "ex": 2, "le": 3, "nu": 4, "dt": 5, "jos": 6, "jue": 7, "rut": 8, "1sa": 9, "2sa": 10,
	"1re": 11, "2re": 12, "1cr": 13, "2cr": 14, "esd": 15, "ne": 16, "est": 17, "job": 18, "sl": 19, "pr": 20, "ec": 21,
	"can": 22, "is": 23, "jer": 24, "lam": 25, "eze": 26, "da": 27, "os": 28, "joe": 29, "am": 30, "abd": 31, "jon": 32,
	"miq": 33, "na": 34, "hab": 35, "sof": 36, "ag": 37, "zac": 38, "mal": 39, "mt": 40, "mr": 41, "lu": 42, "jn": 43,
	"hch": 44, "ro": 45, "1co": 46, "2co": 47, "gal": 48, "ef": 49, "flp": 50, "col": 51, "1ts": 52, "2ts": 53, "1ti": 54,
	"2ti": 55, "tit": 56, "flm": 57, "heb": 58, "snt": 59, "1pe": 60, "2pe": 61, "1jn": 62, "2jn": 63, "3jn": 64, "jud": 65, "ap": 66}

func writtenBook(name string) (string, bool) {
	if n, ok := wbAbbrev[strings.ReplaceAll(fold(name), " ", "")]; ok {
		return bibleOrder[n-1].Name, true
	}
	if n, _, ok := BibleBook(name); ok {
		return bibleOrder[n-1].Name, true
	}
	return "", false
}

// speakWrittenRefs reads references written with full book names
// ("Hebreos 5:1", "1 Juan 4:8") or workbook abbreviations ("Lam 3:22").
// Abbreviations with a dot ("Heb. 5:1") are left to SpeakBibleRefs.
func speakWrittenRefs(s string) string {
	return reWritten.ReplaceAllStringFunc(s, func(m string) string {
		p := reWritten.FindStringSubmatch(m)
		book, ok := writtenBook(p[1])
		if !ok {
			return m
		}
		switch {
		case p[4] != "":
			return fmt.Sprintf("%s %s, versículos %s al %s", book, p[2], p[3], p[4])
		case strings.TrimSpace(p[5]) != "":
			list := strings.Split(strings.ReplaceAll(p[5], " ", ""), ",")[1:]
			return fmt.Sprintf("%s %s, versículos %s", book, p[2], joinES(append([]string{p[3]}, list...)))
		}
		return fmt.Sprintf("%s %s, versículo %s", book, p[2], p[3])
	})
}

func spoken(s string) string {
	s = SpeakBibleRefs(speakWrittenRefs(unshout(s)))
	return reDoubleDot.ReplaceAllString(s, "$1")
}

func numsOf(s string) []int {
	var out []int
	for _, f := range regexp.MustCompile(`\d+`).FindAllString(s, -1) {
		n, _ := strconv.Atoi(f)
		out = append(out, n)
	}
	return out
}

func paraLabel(nums []int) string {
	switch len(nums) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("Párrafo %d.", nums[0])
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return "Párrafos " + strings.Join(parts[:len(parts)-1], ", ") + " y " + parts[len(parts)-1] + "."
}

func articleBody(page string) string {
	if i := strings.Index(page, "<article"); i >= 0 {
		page = page[i:]
	}
	if i := strings.Index(page, "</article>"); i >= 0 {
		page = page[:i]
	}
	return page
}

// ParseStudyArticle turns a Watchtower study article into pieces: an intro
// (title and theme text), one piece per paragraph with its question first,
// and the closing review questions. Picture captions are skipped.
func ParseStudyArticle(page string) (Reading, error) {
	var r Reading
	var theme, sub string
	var qNums []int
	var question string
	pendingFirst := false
	review := false
	var reviewQs []string
	for _, m := range reElem.FindAllStringSubmatch(articleBody(page), -1) {
		tag, class, inner := m[1], elemClass(m[2]), m[3]
		text := cleanHTML(inner)
		if text == "" || tag == "figure" {
			continue
		}
		switch {
		case tag == "h1":
			r.Title = text
		case strings.Contains(class, "themeScrp"):
			theme = text
		case strings.Contains(class, "pubRefs"), strings.Contains(class, "contextTtl"):
		case tag == "h2" && strings.Contains(fold(text), "que responderias"):
			review = true
		case tag == "h2":
			sub = text
		case strings.Contains(class, "qu"):
			lead := reLeadNums.FindStringSubmatch(text)
			if lead == nil {
				continue
			}
			qNums = numsOf(lead[1])
			question = reImageNote.ReplaceAllString(strings.TrimSpace(text[len(lead[0]):]), "")
			pendingFirst = true
		case tag == "li" && review:
			reviewQs = append(reviewQs, strings.TrimSpace(strings.TrimSuffix(text, "Respuesta")))
		case tag == "p" && class == "" && len(qNums) > 0:
			n := 0
			if pm := reParaNum.FindStringSubmatch(text); pm != nil {
				n, _ = strconv.Atoi(pm[1])
				text = text[len(pm[0]):]
			} else if pendingFirst {
				n = qNums[0]
			} else {
				continue // a picture caption or box, not a paragraph
			}
			body := spoken(text)
			if pendingFirst {
				head := paraLabel(qNums) + " " + spoken(question)
				if sub != "" {
					head = spoken(sub) + ". " + head
					sub = ""
				}
				body = head + " " + body
				pendingFirst = false
			}
			r.Pieces = append(r.Pieces, ReadPiece{Para: n, Text: body})
		}
	}
	if r.Title == "" || len(r.Pieces) == 0 {
		return r, errors.New("study article: no paragraphs found")
	}
	intro := "Estudio de La Atalaya. " + r.Title + "."
	if theme != "" {
		intro += " Texto temático: " + spoken(theme)
	}
	r.Pieces = append([]ReadPiece{{Text: intro}}, r.Pieces...)
	if len(reviewQs) > 0 {
		r.Pieces = append(r.Pieces, ReadPiece{Text: "¿Qué responderías? " + strings.Join(reviewQs, " ")})
	}
	return r, nil
}

func workbookText(s string) string {
	s = reMins.ReplaceAllStringFunc(s, func(m string) string {
		n := reMins.FindStringSubmatch(m)[1]
		if n == "1" {
			return "1 minuto."
		}
		return n + " minutos."
	})
	s = strings.ReplaceAll(s, "(1 min.)", "1 minuto.")
	s = reWbPubRef.ReplaceAllString(s, "")
	for i := 0; i < 3; i++ {
		s = reEmptyRef.ReplaceAllStringFunc(s, func(m string) string {
			if strings.HasSuffix(m, ")") && !strings.HasPrefix(m, "(") {
				return ")"
			}
			if strings.HasPrefix(m, "(") && !strings.HasSuffix(m, ")") {
				return "("
			}
			return ""
		})
	}
	s = strings.ReplaceAll(s, " | ", ". ")
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, " .", ".")), " ")
	return spoken(strings.TrimSpace(strings.TrimSuffix(s, "Respuesta")))
}

// ParseWorkbook turns a "Vida y Ministerio" week into one piece per part and
// returns the weekly Bible reading heading.
func ParseWorkbook(page string) (Reading, string, error) {
	var r Reading
	weekly := ""
	var cur []string
	section := ""
	flush := func() {
		if len(cur) > 0 {
			r.Pieces = append(r.Pieces, ReadPiece{Text: strings.Join(cur, " ")})
			cur = nil
		}
	}
	for _, m := range reElem.FindAllStringSubmatch(articleBody(page), -1) {
		tag, inner := m[1], m[3]
		text := cleanHTML(inner)
		if text == "" || tag == "figure" {
			continue
		}
		switch tag {
		case "h1":
			r.Title = text
		case "h2":
			if b, from, to, ok := ParseWeeklyReading(text); ok && weekly == "" && len(r.Pieces) == 0 && len(cur) == 0 {
				weekly = text
				r.Pieces = append(r.Pieces, ReadPiece{Text: "Lectura bíblica de la semana: " + chapterRange(b, from, to) + "."})
				continue
			}
			flush()
			section = unshout(text) + "."
		case "h3":
			flush()
			t := workbookText(text)
			if !strings.HasSuffix(t, ".") {
				t += "."
			}
			if section != "" {
				t = section + " " + t
				section = ""
			}
			cur = append(cur, t)
		case "p", "li":
			if t := workbookText(text); t != "" {
				cur = append(cur, t)
			}
		}
	}
	flush()
	if r.Title == "" || len(r.Pieces) == 0 {
		return r, "", errors.New("workbook: no parts found")
	}
	r.Pieces = append([]ReadPiece{{Text: "Guía de actividades, semana del " + strings.ToLower(r.Title) + "."}}, r.Pieces...)
	return r, weekly, nil
}

// ParseBibleChapter reads verses from..to (0, 0 = whole chapter) of a wol
// Bible chapter page, grouped into pieces of about 300 characters.
func ParseBibleChapter(page string, book, chapter, from, to int) (Reading, error) {
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<span id="v%d-%d-(\d+)-\d+" class="v">(.*?)</span>`, book, chapter))
	var order []int
	verses := map[int][]string{}
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		v, _ := strconv.Atoi(m[1])
		if from > 0 && (v < from || v > to) {
			continue
		}
		t := cleanHTML(reVerseNum.ReplaceAllString(m[2], ""))
		if t == "" {
			continue
		}
		if _, seen := verses[v]; !seen {
			order = append(order, v)
		}
		verses[v] = append(verses[v], t)
	}
	if len(order) == 0 {
		return Reading{}, fmt.Errorf("bible %d:%d verses %d-%d not found", book, chapter, from, to)
	}
	name := bibleOrder[book-1].Name
	title := fmt.Sprintf("%s %d", name, chapter)
	switch {
	case from > 0 && from == to:
		title += fmt.Sprintf(", versículo %d", from)
	case from > 0:
		title += fmt.Sprintf(", versículos %d al %d", from, to)
	}
	r := Reading{Title: title}
	cur, curPara := title+".", order[0]
	for _, v := range order {
		t := strings.Join(verses[v], " ")
		if len(cur) > 300 {
			r.Pieces = append(r.Pieces, ReadPiece{Para: curPara, Text: cur})
			cur, curPara = "", v
		}
		cur = strings.TrimSpace(cur + " " + t)
	}
	r.Pieces = append(r.Pieces, ReadPiece{Para: curPara, Text: cur})
	return r, nil
}

func (j *JW) weekDoc(ctx context.Context, t time.Time, heading string) (string, error) {
	if j.Location != nil {
		t = t.In(j.Location)
	}
	y, w := t.ISOWeek()
	page, err := j.get(ctx, fmt.Sprintf("%s/es/wol/meetings/r4/lp-s/%d/%d", j.WOLBase, y, w))
	if err != nil {
		return "", err
	}
	s := string(page)
	i := strings.Index(s, heading)
	if i < 0 {
		return "", fmt.Errorf("week %d/%d: no %q section", y, w, heading)
	}
	m := regexp.MustCompile(`href="(/es/wol/d/r4/lp-s/\d+)"`).FindStringSubmatch(s[i:])
	if m == nil {
		return "", fmt.Errorf("week %d/%d: no link under %q", y, w, heading)
	}
	doc, err := j.get(ctx, j.WOLBase+m[1])
	return string(doc), err
}

// Watchtower returns the study article of the week that contains t.
func (j *JW) Watchtower(ctx context.Context, t time.Time) (Reading, error) {
	page, err := j.weekDoc(ctx, t, "Estudio de La Atalaya")
	if err != nil {
		return Reading{}, err
	}
	return ParseStudyArticle(page)
}

// Workbook returns the "Vida y Ministerio" week that contains t.
func (j *JW) Workbook(ctx context.Context, t time.Time) (Reading, string, error) {
	page, err := j.weekDoc(ctx, t, "Vida y Ministerio")
	if err != nil {
		return Reading{}, "", err
	}
	return ParseWorkbook(page)
}

// Bible reads a chapter, or verses from..to of it (0, 0 = whole chapter).
func (j *JW) Bible(ctx context.Context, book, chapter, from, to int) (Reading, error) {
	page, err := j.get(ctx, fmt.Sprintf("%s/es/wol/b/r4/lp-s/nwtsty/%d/%d", j.WOLBase, book, chapter))
	if err != nil {
		return Reading{}, err
	}
	return ParseBibleChapter(string(page), book, chapter, from, to)
}

// WeeklyBibleReading reads the chapters of the week's Bible reading.
func (j *JW) WeeklyBibleReading(ctx context.Context, t time.Time) (Reading, error) {
	_, weekly, err := j.Workbook(ctx, t)
	if err != nil {
		return Reading{}, err
	}
	book, from, to, ok := ParseWeeklyReading(weekly)
	if !ok {
		return Reading{}, fmt.Errorf("weekly reading %q not understood", weekly)
	}
	r := Reading{Title: chapterRange(book, from, to)}
	for ch := from; ch <= to; ch++ {
		c, err := j.Bible(ctx, book, ch, 0, 0)
		if err != nil {
			return Reading{}, err
		}
		r.Pieces = append(r.Pieces, c.Pieces...)
	}
	return r, nil
}
