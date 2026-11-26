package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	reBookCtx    = regexp.MustCompile(`^(\d+)\s+(.+)$`)
	reBookLetter = regexp.MustCompile(`\s+[A-Z]$`)
	reBookTitled = regexp.MustCompile(`^(“[^”]+”)\s*\(([^()]*)\)$`)
	reRefSeg     = regexp.MustCompile(`^(\d+):(\d+)(?:-(?:(\d+):)?(\d+))?((?:,\s*\d+)*)$`)
)

// bookRefList reads "Jueces 6:1-7:22; 8:4-23, 28" for the "Lea el relato
// bíblico" box: the references only, not the verses.
func bookRefList(s string) string {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	for i > 0 && s[i-1] != ' ' { // keep a "1 " book prefix inside the name
		i = strings.IndexFunc(s[i+1:], func(r rune) bool { return r >= '0' && r <= '9' }) + i + 1
	}
	if i <= 0 {
		return spoken(s)
	}
	name, rest := strings.TrimSpace(s[:i]), s[i:]
	book, ok := writtenBook(name)
	if !ok {
		return spoken(s)
	}
	var segs []string
	for _, seg := range strings.Split(rest, ";") {
		m := reRefSeg.FindStringSubmatch(strings.TrimSpace(seg))
		if m == nil {
			segs = append(segs, strings.TrimSpace(seg))
			continue
		}
		extra := strings.Split(strings.ReplaceAll(m[5], " ", ""), ",")[1:]
		switch {
		case m[3] != "": // 6:1-7:22
			segs = append(segs, fmt.Sprintf("%s, versículo %s, al %s, versículo %s", m[1], m[2], m[3], m[4]))
		case m[4] != "": // 8:4-23, 28
			t := fmt.Sprintf("%s, versículos %s al %s", m[1], m[2], m[4])
			if len(extra) > 0 {
				t = fmt.Sprintf("%s, versículos %s al %s y %s", m[1], m[2], m[4], joinES(extra))
			}
			segs = append(segs, t)
		case len(extra) > 0:
			segs = append(segs, fmt.Sprintf("%s, versículos %s", m[1], joinES(append([]string{m[2]}, extra...))))
		default:
			segs = append(segs, fmt.Sprintf("%s, versículo %s", m[1], m[2]))
		}
	}
	return book + " " + strings.Join(segs, "; ") + "."
}

// bookLine reads a line of a chapter's boxes: "N." becomes "Punto N.",
// "(Lea …)" and "(Vea …)" become sentences, a parenthesis with only a
// publication becomes "Vea <publication>.", and the answer field and its
// picture letter are dropped.
func bookLine(raw string) string {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "Respuesta"))
	s = reBookLetter.ReplaceAllString(s, "")
	if m := reBookTitled.FindStringSubmatch(s); m != nil {
		if pub, ok := speakPubRef(m[2]); ok {
			return spoken("Lea " + m[1] + ", en " + pub + ".")
		}
		return spoken("Lea " + m[1] + ".")
	}
	if pm := reLeadNums.FindStringSubmatch(s); pm != nil {
		s = "Punto " + strings.TrimSuffix(pm[1], ".") + ". " + s[len(pm[0]):]
	}
	s = reParen.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.TrimSpace(reParen.FindStringSubmatch(m)[1])
		l := strings.ToLower(inner)
		if strings.HasPrefix(l, "lea ") || strings.HasPrefix(l, "vea ") {
			return ". " + inner
		}
		// Only Bible citations and publications: say them as a sentence,
		// "Lea <citas> y vea <publicaciones>", as the chapter audio does.
		var refs, pubs []string
		for _, part := range strings.Split(inner, ";") {
			p := strings.TrimSpace(part)
			if pub, ok := speakPubRef(p); ok {
				pubs = append(pubs, pub)
			} else if reCiteRef.MatchString(p) {
				refs = append(refs, p)
			} else {
				return m
			}
		}
		switch {
		case len(refs) > 0 && len(pubs) > 0:
			return ". Lea " + strings.Join(refs, " y ") + " y vea " + strings.Join(pubs, " y ")
		case len(refs) > 0:
			return ". Lea " + strings.Join(refs, " y ")
		case len(pubs) > 0:
			return ". Vea " + strings.Join(pubs, " y ")
		}
		return m
	})
	s = reWbInline.ReplaceAllStringFunc(s, func(m string) string {
		if sp, ok := speakPubRef(m); ok {
			return sp
		}
		return m
	})
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " .", ".")
	s = reDoubleDot.ReplaceAllString(s, "$1")
	if s != "" && !strings.ContainsAny(s[len(s)-1:], ".?!:") {
		s += "."
	}
	return spoken(s)
}

// ParseBookChapter turns a chapter of the congregation book study into
// pieces read like its official audio: the chapter title, the story, its
// pictures, then each box, and "Fin del capítulo".
func ParseBookChapter(page string) (Reading, error) {
	var r Reading
	num, name := "", ""
	var cur []string
	curPause := false
	inBox, refBox := false, false
	flush := func() {
		if len(cur) > 0 {
			r.Pieces = append(r.Pieces, ReadPiece{Text: strings.Join(cur, " "), Pause: curPause})
			cur, curPause = nil, false
		}
	}
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
			if idx >= 0 && idx < len(figures) {
				if img, ok := figurePiece(figures[idx], nil, "La imagen muestra"); ok {
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
		switch {
		case strings.Contains(class, "contextTtl"):
			if cm := reBookCtx.FindStringSubmatch(text); cm != nil {
				num, name = cm[1], capitalize(strings.ToLower(cm[2]))
			}
		case tag == "h1":
			r.Title = text
		case tag == "h2" || tag == "h3":
			flush()
			inBox, curPause = true, true
			refBox = strings.Contains(fold(text), "lea el relato")
			cur = append(cur, spoken(strings.TrimSuffix(text, ".")+"."))
			if strings.HasSuffix(text, "?") {
				cur[len(cur)-1] = spoken(text)
			}
		case !inBox && tag == "p":
			flush()
			cur = append(cur, spoken(trimCitations(text)))
			flush()
		case refBox:
			cur = append(cur, bookRefList(text))
		default:
			if t := bookLine(text); t != "" && t != "." {
				cur = append(cur, t)
			}
		}
	}
	flush()
	if r.Title == "" || len(r.Pieces) == 0 {
		return r, errors.New("book chapter: no text found")
	}
	if num != "" {
		r.Title = "Capítulo " + num + ". " + name + ". " + r.Title
	}
	r.Pieces = append([]ReadPiece{{Text: r.Title + "."}}, r.Pieces...)
	end := "Fin del capítulo."
	if num != "" {
		end = "Fin del capítulo " + num + "."
	}
	r.Pieces = append(r.Pieces, ReadPiece{Text: end, Pause: true})
	return r, nil
}

var reBookLink = regexp.MustCompile(`href="(/es/wol/(?:pc|d)/r4/lp-s/[^"#]+)"`)

// BookStudy reads this week's congregation book study chapter, found
// through the workbook's "Estudio bíblico de la congregación" part.
func (j *JW) BookStudy(ctx context.Context, t time.Time) (Reading, error) {
	page, err := j.weekDoc(ctx, t, "Vida y Ministerio")
	if err != nil {
		return Reading{}, err
	}
	i := strings.Index(page, "Estudio bíblico de la congregación")
	if i < 0 {
		return Reading{}, errors.New("workbook: no congregation Bible study part")
	}
	m := reBookLink.FindStringSubmatch(page[i:])
	if m == nil {
		return Reading{}, errors.New("workbook: no link to the book study chapter")
	}
	ch, err := j.get(ctx, j.WOLBase+m[1])
	if err != nil {
		return Reading{}, err
	}
	return ParseBookChapter(string(ch))
}
