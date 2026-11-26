package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ReadPiece is one spoken unit of a long reading. Para is the article
// paragraph or first Bible verse it starts at, 0 for intros and reviews.
type ReadPiece struct {
	Para  int
	Text  string
	Reads []BibleRef // texts marked "lee": read aloud where they are cited
	Pause bool       // short silence before this piece
}

// BibleRef is a chapter and verse range.
type BibleRef struct {
	Book, Chapter, From, To int
	Notes                   bool // "lee ... y la nota": read the verse footnote too
}

// leeMark stands where a "lee" citation was cut out of a paragraph; the
// verse is read after that sentence.
const leeMark = "⟦lee⟧"

var (
	reLee     = regexp.MustCompile(`(?s)<strong>\s*\(?\s*lee\s*</strong>\s*((?:<a[^>]*class="b"[^>]*>.*?</a>(?:\s*(?:,|;|\s+y)\s*)?)+)((?:\s*<strong>[^<]*</strong>)?)`)
	reLeeLink = regexp.MustCompile(`(?s)<a[^>]*class="b"[^>]*>(.*?)</a>`)
	reRefText = regexp.MustCompile(`^(.+?)\s+(\d+):(\d+)(?:\s*[-–]\s*(\d+)|((?:,\s*\d+)+))?$`)
)

// parseRef reads "Hebreos 7:25", "Heb. 4:15, 16" or "1 Juan 2:1-3".
func parseRef(text string) (BibleRef, bool) {
	m := reRefText.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return BibleRef{}, false
	}
	name := strings.TrimSuffix(m[1], ".")
	book := 0
	if n, ok := wbAbbrev[strings.ReplaceAll(fold(name), " ", "")]; ok {
		book = n
	} else if n, _, ok := BibleBook(name); ok {
		book = n
	} else if full, ok := bibleBooks[m[1]]; ok {
		book, _, _ = BibleBook(full)
	}
	if book == 0 {
		return BibleRef{}, false
	}
	ch, _ := strconv.Atoi(m[2])
	from, _ := strconv.Atoi(m[3])
	to := from
	if m[4] != "" {
		to, _ = strconv.Atoi(m[4])
	} else if m[5] != "" {
		nums := numsOf(m[5])
		to = nums[len(nums)-1]
	}
	return BibleRef{Book: book, Chapter: ch, From: from, To: to}, true
}

var (
	reParen   = regexp.MustCompile(`\s*\(([^()]*)\)`)
	reCiteRef = regexp.MustCompile(`^(?:[123]\s)?[\p{L}.]+\.?(?:\s[\p{L}]+)*\s\d+:\d+`)
)

// trimCitations keeps a parenthesized citation only when it names the
// source of words quoted right before it (“...” (1 Juan 2:1)); citations
// that only allude to a text (... nosotros (Sal. 86:5).) are dropped.
// Texts marked "lee" and parentheses that are not citations stay.
func trimCitations(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range reParen.FindAllStringSubmatchIndex(s, -1) {
		start, end, inner := m[0], m[1], s[m[2]:m[3]]
		// Quoted words earlier in the same sentence make it a source.
		sentence := s[:start]
		for _, sep := range []string{". ", "? ", "! "} {
			if k := strings.LastIndex(sentence, sep); k >= 0 {
				sentence = sentence[k+len(sep):]
			}
		}
		quoted := strings.ContainsAny(sentence, "”»") || strings.Count(sentence, "\"") >= 2
		var keep []string
		cites := false
		leeHere := 0
		for _, part := range strings.Split(inner, ";") {
			p := strings.TrimSpace(part)
			lp := strings.ToLower(p)
			switch {
			case strings.HasPrefix(lp, "lee "):
				cites = true
				leeHere++
			case strings.HasPrefix(lp, "mira también"), strings.HasPrefix(lp, "vea también"), strings.HasPrefix(lp, "ve también"):
				cites = true
			case reCiteRef.MatchString(p):
				cites = true
				if quoted {
					keep = append(keep, p)
				}
			default:
				keep = append(keep, p)
			}
		}
		b.WriteString(s[last:start])
		switch {
		case !cites:
			b.WriteString(s[start:end])
		case len(keep) > 0:
			b.WriteString(s[start:m[2]-1] + "(" + strings.Join(keep, "; ") + ")")
		}
		b.WriteString(strings.Repeat(leeMark, leeHere))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// leeRefs returns the texts marked with a bold "lee" in a paragraph.
func leeRefs(raw string) []BibleRef {
	var out []BibleRef
	for _, m := range reLee.FindAllStringSubmatch(raw, -1) {
		notes := strings.Contains(fold(cleanHTML(m[2])), "nota")
		for _, l := range reLeeLink.FindAllStringSubmatch(m[1], -1) {
			if r, ok := parseRef(cleanHTML(l[1])); ok {
				r.Notes = notes
				out = append(out, r)
			}
		}
	}
	return out
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
		core := strings.TrimLeft(lw, "(¿¡“\"«")
		if _, ok := bibleBooks[capitalize(core)]; ok { // "(HEB." -> "(Heb."
			words[i] = lw[:len(lw)-len(core)] + capitalize(core)
			continue
		}
		if p, ok := properNouns[fold(strings.Trim(lw, ".,;:¿?¡!"))]; ok {
			lw = strings.Replace(lw, strings.ToLower(p), p, 1)
		}
		if i == 0 || strings.ContainsAny(words[i-1][len(words[i-1])-1:], ".?!") {
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
		return fmt.Sprintf("Párrafo %d. Pregunta:", nums[0])
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return "Párrafos " + strings.Join(parts[:len(parts)-1], ", ") + " y " + parts[len(parts)-1] + ". Pregunta:"
}

var (
	reSong     = regexp.MustCompile(`(?i)^canción (\d+)\s+(.+)$`)
	reWeek1    = regexp.MustCompile(`(?i)^(\d+)-(\d+) de (\p{L}+)`)
	reWeek2    = regexp.MustCompile(`(?i)^(\d+) de (\p{L}+)\s*-\s*(\d+) de (\p{L}+)`)
	reAlt      = regexp.MustCompile(`alt="([^"]*)"`)
	reFigure   = regexp.MustCompile(`(?s)<figure\b.*?</figure>`)
	reMiraPara = regexp.MustCompile(`\s*\((?:mira|vea|ve) (?:el párrafo|los párrafos) (\d+)[^)]*\)`)
)

// weekPhrase turns "23-29 DE NOVIEMBRE DE 2026" into "del 23 al 29 de noviembre".
func weekPhrase(s string) string {
	if m := reWeek2.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("del %s de %s al %s de %s", m[1], strings.ToLower(m[2]), m[3], strings.ToLower(m[4]))
	}
	if m := reWeek1.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("del %s al %s de %s", m[1], m[2], strings.ToLower(m[3]))
	}
	return ""
}

var articleWords = map[string]bool{"un": true, "una": true, "unos": true, "unas": true, "el": true, "la": true, "los": true, "las": true, "dos": true, "tres": true, "varios": true, "varias": true}

// figurePiece describes a picture the way the article audio does: its
// description, then its caption. It belongs to the paragraph the caption
// points at, or to the last question's paragraph.
func figurePiece(inner string, qNums []int, seeing string) (ReadPiece, bool) {
	alt := ""
	if m := reAlt.FindStringSubmatch(inner); m != nil {
		alt = stripHTML(m[1])
	}
	caption := ""
	if m := regexp.MustCompile(`(?s)<figcaption[^>]*>(.*?)</figcaption>`).FindStringSubmatch(inner); m != nil {
		caption = cleanHTML(m[1])
	}
	if alt == "" && caption == "" {
		return ReadPiece{}, false
	}
	para := 0
	if len(qNums) > 0 {
		para = qNums[0]
	}
	if m := reMiraPara.FindStringSubmatch(caption); m != nil {
		para, _ = strconv.Atoi(m[1])
		caption = strings.TrimSpace(reMiraPara.ReplaceAllString(caption, ""))
	}
	var parts []string
	if alt != "" {
		if rest, ok := strings.CutPrefix(alt, "Serie de imágenes:"); ok {
			parts = append(parts, "Aquí se muestra una serie de imágenes: "+strings.TrimSpace(rest))
		} else if rest, ok := strings.CutPrefix(alt, "Imagen del video"); ok {
			parts = append(parts, "Aquí vemos una imagen del video"+rest)
		} else {
			first, _, _ := strings.Cut(alt, " ")
			if articleWords[strings.ToLower(first)] {
				alt = strings.ToLower(alt[:len(first)]) + alt[len(first):]
				parts = append(parts, seeing+" que "+alt)
			} else {
				parts = append(parts, seeing+": "+alt)
			}
		}
	}
	if caption != "" {
		parts = append(parts, "El comentario dice: "+caption)
	}
	text := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !strings.HasSuffix(p, ".") {
			p += "."
		}
		text = strings.TrimSpace(text + " " + p)
	}
	return ReadPiece{Para: para, Text: spoken(text), Pause: true}, true
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
	var theme, sub, week, song, endSong, topic string
	wantTopic := false
	var qNums []int
	var question string
	pendingFirst := false
	review := false
	var reviewQs []string
	// Pull figures out first: their captions hold <p> elements that would
	// end a figure match early.
	var figures []string
	body := reFigure.ReplaceAllStringFunc(articleBody(page), func(f string) string {
		figures = append(figures, f)
		return fmt.Sprintf(`<figure data-i="%d"></figure>`, len(figures)-1)
	})
	for _, m := range reElem.FindAllStringSubmatch(body, -1) {
		tag, class, inner := m[1], elemClass(m[2]), m[3]
		if tag == "figure" {
			var idx int
			fmt.Sscanf(m[2], ` data-i="%d"`, &idx)
			if idx < 0 || idx >= len(figures) {
				continue
			}
			if img, ok := figurePiece(figures[idx], qNums, "En la imagen para este párrafo se ve"); ok {
				r.Pieces = append(r.Pieces, img)
			}
			continue
		}
		text := cleanHTML(inner)
		if text == "" {
			continue
		}
		switch {
		case tag == "h1":
			r.Title = text
		case strings.Contains(class, "themeScrp"):
			theme = text
		case strings.Contains(class, "contextTtl"):
			week = weekPhrase(text)
		case strings.Contains(class, "pubRefs"):
			switch {
			case reSong.MatchString(text):
				sm := reSong.FindStringSubmatch(text)
				line := "Canción " + sm[1] + ": " + sm[2] + "."
				if len(r.Pieces) == 0 {
					song = line
				} else {
					endSong = line
				}
			case strings.EqualFold(text, "TEMA"):
				wantTopic = true
			case wantTopic:
				topic, wantTopic = text, false
			}
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
			question = trimCitations(reImageNote.ReplaceAllString(strings.TrimSpace(text[len(lead[0]):]), ""))
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
			body := spoken(trimCitations(text))
			if pendingFirst {
				head := paraLabel(qNums) + " " + spoken(question)
				if sub != "" {
					head = spoken(sub) + ". " + head
					sub = ""
				}
				body = head + " " + body
				pendingFirst = false
			}
			r.Pieces = append(r.Pieces, ReadPiece{Para: n, Text: body, Reads: leeRefs(inner)})
		}
	}
	if r.Title == "" || len(r.Pieces) == 0 {
		return r, errors.New("study article: no paragraphs found")
	}
	intro := ""
	if week != "" {
		intro = "El siguiente artículo se estudiará durante la semana " + week + ". "
	}
	intro += r.Title + "."
	if theme != "" {
		intro += " Texto temático: " + spoken(theme)
	}
	if song != "" {
		intro += " " + song
	}
	if topic != "" {
		t := strings.TrimSpace(topic)
		if !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, "?") {
			t += "."
		}
		intro += " Tema: " + t
	}
	r.Pieces = append([]ReadPiece{{Text: intro}}, r.Pieces...)
	closing := ""
	if len(reviewQs) > 0 {
		closing = "¿Qué responderías? " + strings.Join(reviewQs, " ")
	}
	if endSong != "" {
		closing = strings.TrimSpace(closing + " " + endSong)
	}
	r.Pieces = append(r.Pieces, ReadPiece{Text: strings.TrimSpace(closing + " Fin del artículo.")})
	return r, nil
}

var monthsES = []string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

var (
	rePubMonthly = regexp.MustCompile(`^(w|g)(\d{2})\.(\d{2}) (\d+(?:-\d+)?)(?: párrs?\. ([\d, -]+))?$`)
	rePubDated   = regexp.MustCompile(`^(w|g)(\d{2}) (\d{1,2})/(\d{1,2}) (\d+(?:-\d+)?)(?: párrs?\. ([\d, -]+))?$`)
	rePubTh      = regexp.MustCompile(`^th lección (\d+)$`)
	rePubLmd     = regexp.MustCompile(`^lmd lección (\d+)(?: punto (\d+))?$`)
	rePubWcg     = regexp.MustCompile(`^wcg (?:cap|lec)\. (\d+)$`)
	rePubIt      = regexp.MustCompile(`^it “([^”]+)” párrs?\. ([\d, -]+)$`)
)

func pubYear(yy string) string {
	n, _ := strconv.Atoi(yy)
	if n <= 50 {
		return strconv.Itoa(2000 + n)
	}
	return strconv.Itoa(1900 + n)
}

func pubMagazine(code string) string {
	if code == "g" {
		return "¡Despertad!"
	}
	return "La Atalaya"
}

// pubNums reads "30" as "página 30" and "26-30" as "páginas 26 a 30".
func pubNums(one, many, s string) string {
	s = strings.TrimSpace(s)
	if a, b, ok := strings.Cut(s, "-"); ok {
		return many + " " + strings.TrimSpace(a) + " a " + strings.TrimSpace(b)
	}
	if strings.Contains(s, ",") {
		parts := strings.Split(strings.ReplaceAll(s, " ", ""), ",")
		return many + " " + joinES(parts)
	}
	return one + " " + s
}

// speakPubRef reads a publication reference the way the meeting audio does
// ("w24.01 30 párr. 16" -> "La Atalaya de enero de 2024, página 30, párrafo 16").
func speakPubRef(s string) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\u00a0", " "))
	out := ""
	if m := rePubMonthly.FindStringSubmatch(s); m != nil {
		mo, _ := strconv.Atoi(m[3])
		if mo < 1 || mo > 12 {
			return "", false
		}
		out = fmt.Sprintf("%s de %s de %s, %s", pubMagazine(m[1]), monthsES[mo], pubYear(m[2]), pubNums("página", "páginas", m[4]))
		if m[5] != "" {
			out += ", " + pubNums("párrafo", "párrafos", m[5])
		}
		return out, true
	}
	if m := rePubDated.FindStringSubmatch(s); m != nil {
		mo, _ := strconv.Atoi(m[4])
		if mo < 1 || mo > 12 {
			return "", false
		}
		out = fmt.Sprintf("%s del %s de %s de %s, %s", pubMagazine(m[1]), m[3], monthsES[mo], pubYear(m[2]), pubNums("página", "páginas", m[5]))
		if m[6] != "" {
			out += ", " + pubNums("párrafo", "párrafos", m[6])
		}
		return out, true
	}
	if m := rePubTh.FindStringSubmatch(s); m != nil {
		return "Analiza el folleto Maestros, lección " + m[1], true
	}
	if m := rePubLmd.FindStringSubmatch(s); m != nil {
		out = "Analiza el folleto Una obra de amor, lección " + m[1]
		if m[2] != "" {
			out += ", punto " + m[2]
		}
		return out, true
	}
	if m := rePubWcg.FindStringSubmatch(s); m != nil {
		return "Seamos valientes, capítulo " + m[1], true
	}
	if m := rePubIt.FindStringSubmatch(s); m != nil {
		return "Perspicacia, entrada " + m[1] + ", " + pubNums("párrafo", "párrafos", m[2]), true
	}
	return "", false
}

var (
	reWbMins    = regexp.MustCompile(`\s*\((\d+) mins?\.\)`)
	reWbLeadRef = regexp.MustCompile(`^((?:[123] )?\p{L}+\.? \d+:\d+(?:-\d+)?)\.\s+`)
	reWbInline  = regexp.MustCompile(`\b(?:(?:w|g)\d{2}(?:\.\d{2}| \d{1,2}/\d{1,2}) \d+(?:-\d+)?(?: párrs?\. [\d-]+(?:, \d+)*)?|th lección \d+|lmd lección \d+(?: punto \d+)?|wcg (?:cap|lec)\. \d+)`)
)

// workbookLine reads one workbook line like the meeting audio: minutes
// spoken, Bible citations in parentheses (and a citation opening a
// question) read aloud after the sentence (leeMark), publication
// references spoken in full after them.
func workbookLine(raw string) (string, []BibleRef) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "Respuesta"))
	s = reWbMins.ReplaceAllStringFunc(s, func(m string) string {
		n := reWbMins.FindStringSubmatch(m)[1]
		if n == "1" {
			return ". 1 minuto."
		}
		return ". " + n + " minutos."
	})
	s = strings.TrimSpace(strings.TrimPrefix(s, ". "))
	var refs []BibleRef
	if m := reWbLeadRef.FindStringSubmatch(s); m != nil {
		if r, ok := parseRef(m[1]); ok {
			refs = append(refs, r)
			s = s[len(m[0]):]
		}
	}
	var pubs []string
	s = reParen.ReplaceAllStringFunc(s, func(m string) string {
		inner := reParen.FindStringSubmatch(m)[1]
		var keep []string
		for _, part := range strings.Split(inner, ";") {
			p := strings.TrimSpace(part)
			if sp, ok := speakPubRef(p); ok {
				pubs = append(pubs, sp)
				continue
			}
			if reCiteRef.MatchString(p) {
				if r, ok := parseRef(p); ok {
					refs = append(refs, r)
					continue
				}
			}
			keep = append(keep, p)
		}
		if len(keep) == 0 {
			return ""
		}
		return " (" + strings.Join(keep, "; ") + ")"
	})
	s = reWbInline.ReplaceAllStringFunc(s, func(m string) string {
		if sp, ok := speakPubRef(m); ok {
			return sp
		}
		return m
	})
	s = strings.ReplaceAll(s, " | ", ". ")
	s = strings.Join(strings.Fields(s), " ")
	s = reDoubleDot.ReplaceAllString(s, "$1")
	if s != "" && !strings.ContainsAny(s[len(s)-1:], ".?!:") {
		s += "."
	}
	s += strings.Repeat(leeMark, len(refs))
	if len(pubs) > 0 {
		s += " " + strings.Join(pubs, ". ") + "."
	}
	return spoken(s), refs
}

// ParseWorkbook turns a "Vida y Ministerio" week into one piece per part,
// read like the meeting audio, and returns the weekly Bible reading heading.
func ParseWorkbook(page string) (Reading, string, error) {
	var r Reading
	weekly, weeklyLine := "", ""
	var cur []string
	var curReads []BibleRef
	section := ""
	flush := func() {
		if len(cur) > 0 {
			r.Pieces = append(r.Pieces, ReadPiece{Text: strings.Join(cur, " "), Reads: curReads})
			cur, curReads = nil, nil
		}
	}
	var figures []string
	body := reFigure.ReplaceAllStringFunc(articleBody(page), func(f string) string {
		figures = append(figures, f)
		return fmt.Sprintf(`<figure data-i="%d"></figure>`, len(figures)-1)
	})
	for _, m := range reElem.FindAllStringSubmatch(body, -1) {
		tag, inner := m[1], m[3]
		if tag == "figure" {
			var idx int
			fmt.Sscanf(m[2], ` data-i="%d"`, &idx)
			if idx >= 0 && idx < len(figures) {
				if img, ok := figurePiece(figures[idx], nil, "En la imagen se ve"); ok {
					img.Para = 0
					flush()
					r.Pieces = append(r.Pieces, img)
				}
			}
			continue
		}
		text := cleanHTML(inner)
		if text == "" {
			continue
		}
		switch tag {
		case "h1":
			r.Title = text
		case "h2":
			if b, from, to, ok := ParseWeeklyReading(text); ok && weekly == "" && len(r.Pieces) == 0 && len(cur) == 0 {
				weekly = text
				weeklyLine = " Lectura semanal de la Biblia: " + chapterRange(b, from, to) + "."
				continue
			}
			flush()
			section = unshout(text) + "."
		case "h3":
			flush()
			if pm := reLeadNums.FindStringSubmatch(text); pm != nil {
				text = "Punto " + strings.TrimSuffix(pm[1], ".") + ". " + text[len(pm[0]):]
			}
			t, refs := workbookLine(text)
			if section != "" {
				t = section + " " + t
				section = ""
			}
			cur, curReads = append(cur, t), append(curReads, refs...)
		case "p", "li":
			if t, refs := workbookLine(text); t != "" && t != "." {
				cur, curReads = append(cur, t), append(curReads, refs...)
			}
		}
	}
	flush()
	if r.Title == "" || len(r.Pieces) == 0 {
		return r, "", errors.New("workbook: no parts found")
	}
	intro := "Guía de actividades."
	if w := weekPhrase(r.Title); w != "" {
		intro = "Semana " + w + "."
	}
	r.Pieces = append([]ReadPiece{{Text: intro + weeklyLine}}, r.Pieces...)
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
	for i, v := range order {
		t := strings.Join(verses[v], " ")
		if i == 0 {
			t = strings.TrimLeft(t, "”’ ") // closing quote of an earlier verse
		}
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
	r, err := ParseStudyArticle(page)
	if err != nil {
		return r, err
	}
	return j.resolveReads(ctx, r), nil
}

// resolveReads replaces each lee mark with the cited verse, as the meeting
// audio does: after its sentence, "<ref> dice:" and the text, after a pause.
func (j *JW) resolveReads(ctx context.Context, r Reading) Reading {
	var out []ReadPiece
	for _, p := range r.Pieces {
		if len(p.Reads) == 0 {
			out = append(out, p)
			continue
		}
		rest := p.Text
		first := true
		for _, ref := range p.Reads {
			before, after := splitAtMark(rest)
			if before != "" {
				out = append(out, ReadPiece{Para: p.Para, Text: before, Pause: !first && p.Para > 0})
			}
			first = false
			rest = after
			b, err := j.BibleRef(ctx, ref)
			if err != nil {
				logf("wol cited text %+v: %v", ref, err)
				continue
			}
			lead := b.Title + ", dice:"
			if ref.Notes {
				lead = b.Title + ", y su nota dicen:"
			}
			for k, bp := range b.Pieces {
				t := bp.Text
				if k == 0 {
					t = lead + " " + strings.TrimSpace(strings.TrimPrefix(t, b.Title+"."))
				}
				out = append(out, ReadPiece{Para: p.Para, Text: t, Pause: k == 0})
			}
		}
		if rest = strings.TrimSpace(strings.ReplaceAll(rest, leeMark, "")); rest != "" {
			out = append(out, ReadPiece{Para: p.Para, Text: rest, Pause: !first && p.Para > 0})
		}
	}
	r.Pieces = out
	return r
}

// splitAtMark cuts a paragraph after the sentence holding the first lee
// mark, dropping the mark. Without a mark the whole text comes first.
func splitAtMark(text string) (string, string) {
	i := strings.Index(text, leeMark)
	if i < 0 {
		return strings.TrimSpace(text), ""
	}
	head := text[:i]
	rest := text[i+len(leeMark):]
	if h := strings.TrimSpace(head); h == "" || strings.ContainsAny(h[len(h)-1:], ".?!") {
		return h, strings.TrimSpace(rest) // the mark already sits after a sentence
	}
	end := len(rest)
	for _, sep := range []string{". ", "? ", "! "} {
		if k := strings.Index(rest, sep); k >= 0 && k+1 < end {
			end = k + 1
		}
	}
	return strings.TrimSpace(head + rest[:end]), strings.TrimSpace(rest[end:])
}

// Workbook returns the "Vida y Ministerio" week that contains t.
func (j *JW) Workbook(ctx context.Context, t time.Time) (Reading, string, error) {
	page, err := j.weekDoc(ctx, t, "Vida y Ministerio")
	if err != nil {
		return Reading{}, "", err
	}
	r, weekly, err := ParseWorkbook(page)
	if err != nil {
		return r, weekly, err
	}
	return j.resolveReads(ctx, r), weekly, nil
}

// Bible reads a chapter, or verses from..to of it (0, 0 = whole chapter).
func (j *JW) Bible(ctx context.Context, book, chapter, from, to int) (Reading, error) {
	page, err := j.get(ctx, fmt.Sprintf("%s/es/wol/b/r4/lp-s/nwtsty/%d/%d", j.WOLBase, book, chapter))
	if err != nil {
		return Reading{}, err
	}
	return ParseBibleChapter(string(page), book, chapter, from, to)
}

var reFnLink = regexp.MustCompile(`<a[^>]*class="fn(?: [^"]*)?"[^>]*href="([^"]+)"[^>]*>\*</a>`)

// BibleRef reads a reference; with Notes it uses the regular edition, whose
// footnotes ("O “libertad para hablar”") are read where they are marked.
func (j *JW) BibleRef(ctx context.Context, ref BibleRef) (Reading, error) {
	if !ref.Notes {
		return j.Bible(ctx, ref.Book, ref.Chapter, ref.From, ref.To)
	}
	page, err := j.get(ctx, fmt.Sprintf("%s/es/wol/b/r4/lp-s/nwt/%d/%d", j.WOLBase, ref.Book, ref.Chapter))
	if err != nil {
		return Reading{}, err
	}
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<span id="v%d-%d-(\d+)-\d+" class="v">.*?</span>`, ref.Book, ref.Chapter))
	s := string(page)
	notes := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.Atoi(m[1])
		if v < ref.From || v > ref.To {
			continue
		}
		for _, l := range reFnLink.FindAllStringSubmatch(m[0], -1) {
			if _, done := notes[l[0]]; done {
				continue
			}
			fn, err := j.get(ctx, j.WOLBase+l[1])
			if err != nil {
				logf("wol footnote %s: %v", l[1], err)
				continue
			}
			body := articleBody(string(fn))
			if k := strings.Index(body, "<h1"); k >= 0 {
				if e := strings.Index(body[k:], "</h1>"); e >= 0 {
					body = body[k+e+5:]
				}
			}
			t := strings.TrimSpace(stripHTML(body))
			if t != "" {
				t = strings.TrimSuffix(t, ".")
				r := []rune(t)
				notes[l[0]] = " (" + strings.ToLower(string(r[:1])) + string(r[1:]) + ")"
			}
		}
	}
	for link, text := range notes {
		s = strings.ReplaceAll(s, link, html.EscapeString(text))
	}
	return ParseBibleChapter(s, ref.Book, ref.Chapter, ref.From, ref.To)
}

// WeeklyBibleReading reads the chapters of the week's Bible reading.
func (j *JW) WeeklyBibleReading(ctx context.Context, t time.Time) (Reading, error) {
	page, err := j.weekDoc(ctx, t, "Vida y Ministerio")
	if err != nil {
		return Reading{}, err
	}
	_, weekly, err := ParseWorkbook(page)
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
