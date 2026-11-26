package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// JW fetches public content from jw.org / wol.jw.org.
type JW struct {
	HTTP     *http.Client
	Location *time.Location
	WOLBase  string // https://wol.jw.org
	Mediator string // https://b.jw-cdn.org/apis/mediator/v1
}

func (j *JW) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux armv7l) puente/1.0")
	resp, err := j.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

var (
	reTag      = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace    = regexp.MustCompile(`\s+`)
	rePubRef   = regexp.MustCompile(`(?s)<a href="/es/wol/pc/[^"]*">.*?</a>`)
	reH2       = regexp.MustCompile(`(?s)<h2[^>]*>(.*?)</h2>`)
	reTheme    = regexp.MustCompile(`(?s)<p[^>]*class="themeScrp"[^>]*>(.*?)</p>`)
	reBodyPara = regexp.MustCompile(`(?s)<div class="bodyTxt">(.*?)</div>`)
)

func stripHTML(s string) string {
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

// ParseDailyText extracts the text for date (YYYY-MM-DD) from a wol "h" page.
func ParseDailyText(page, date string) (string, error) {
	marker := `<div class="tabContent" data-date="` + date
	i := strings.Index(page, marker)
	if i < 0 {
		return "", errors.New("date not found on page")
	}
	block := page[i+len(marker):]
	if k := strings.Index(block, `<div class="tabContent"`); k >= 0 {
		block = block[:k]
	}
	block = rePubRef.ReplaceAllString(block, "")
	var parts []string
	if m := reH2.FindStringSubmatch(block); m != nil {
		parts = append(parts, stripHTML(m[1])+".")
	}
	if m := reTheme.FindStringSubmatch(block); m != nil {
		parts = append(parts, stripHTML(m[1]))
	}
	if m := reBodyPara.FindStringSubmatch(block); m != nil {
		parts = append(parts, stripHTML(m[1]))
	}
	if len(parts) < 2 {
		return "", errors.New("daily text not found in block")
	}
	return SpeakBibleRefs(strings.Join(parts, " ")), nil
}

func (j *JW) DailyText(ctx context.Context, date time.Time) (string, error) {
	url := fmt.Sprintf("%s/es/wol/h/r4/lp-s/%d/%d/%d", j.WOLBase, date.Year(), int(date.Month()), date.Day())
	b, err := j.get(ctx, url)
	if err != nil {
		return "", err
	}
	return ParseDailyText(string(b), date.Format("2006-01-02"))
}

// Bible book abbreviations used by the Spanish NWT on wol.jw.org.
var bibleBooks = map[string]string{
	"Gén.": "Génesis", "Éx.": "Éxodo", "Lev.": "Levítico", "Núm.": "Números", "Deut.": "Deuteronomio",
	"Jos.": "Josué", "Juec.": "Jueces", "Rut": "Rut", "1 Sam.": "Primero de Samuel", "2 Sam.": "Segundo de Samuel",
	"1 Rey.": "Primero de Reyes", "2 Rey.": "Segundo de Reyes", "1 Crón.": "Primero de Crónicas", "2 Crón.": "Segundo de Crónicas",
	"Esd.": "Esdras", "Neh.": "Nehemías", "Est.": "Ester", "Job": "Job", "Sal.": "Salmo", "Prov.": "Proverbios",
	"Ecl.": "Eclesiastés", "Cant.": "El Cantar de los Cantares", "Is.": "Isaías", "Jer.": "Jeremías", "Lam.": "Lamentaciones",
	"Ezeq.": "Ezequiel", "Dan.": "Daniel", "Os.": "Oseas", "Joel": "Joel", "Amós": "Amós", "Abd.": "Abdías",
	"Jon.": "Jonás", "Miq.": "Miqueas", "Nah.": "Nahúm", "Hab.": "Habacuc", "Sof.": "Sofonías", "Ag.": "Ageo",
	"Zac.": "Zacarías", "Mal.": "Malaquías", "Mat.": "Mateo", "Mar.": "Marcos", "Luc.": "Lucas", "Juan": "Juan",
	"Hech.": "Hechos", "Rom.": "Romanos", "1 Cor.": "Primera a los Corintios", "2 Cor.": "Segunda a los Corintios",
	"Gál.": "Gálatas", "Efes.": "Efesios", "Filip.": "Filipenses", "Col.": "Colosenses",
	"1 Tes.": "Primera a los Tesalonicenses", "2 Tes.": "Segunda a los Tesalonicenses", "1 Tim.": "Primera a Timoteo",
	"2 Tim.": "Segunda a Timoteo", "Tito": "Tito", "Filem.": "Filemón", "Heb.": "Hebreos", "Sant.": "Santiago",
	"1 Ped.": "Primera de Pedro", "2 Ped.": "Segunda de Pedro", "1 Juan": "Primera de Juan", "2 Juan": "Segunda de Juan",
	"3 Juan": "Tercera de Juan", "Judas": "Judas", "Apoc.": "Apocalipsis",
}

var reBibleRef = func() *regexp.Regexp {
	var keys []string
	for k := range bibleBooks {
		keys = append(keys, regexp.QuoteMeta(k))
	}
	// Longest first so "1 Juan" wins over "Juan".
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if len(keys[j]) > len(keys[i]) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return regexp.MustCompile(`(` + strings.Join(keys, "|") + `) (\d+):(\d+)(?:-(\d+))?((?:, ?\d+)*)`)
}()

// SpeakBibleRefs turns "Sal. 73:23-28" into "Salmo 73, versículos 23 al 28".
func SpeakBibleRefs(s string) string {
	s = reBibleRef.ReplaceAllStringFunc(s, func(m string) string {
		p := reBibleRef.FindStringSubmatch(m)
		book, ch, v1, v2, more := bibleBooks[p[1]], p[2], p[3], p[4], strings.TrimSpace(p[5])
		switch {
		case v2 != "":
			return fmt.Sprintf("%s %s, versículos %s al %s", book, ch, v1, v2)
		case more != "":
			list := strings.Split(strings.ReplaceAll(more, " ", ""), ",")[1:]
			return fmt.Sprintf("%s %s, versículos %s", book, ch, joinES(append([]string{v1}, list...)))
		}
		return fmt.Sprintf("%s %s, versículo %s", book, ch, v1)
	})
	s = strings.ReplaceAll(s, "( ", "(")
	return strings.ReplaceAll(s, " )", ")")
}

// Track is one playable audio item.
type Track struct {
	Title    string
	Artist   string  // empty when unknown
	URL      string  // http(s) MP3, or yt:<videoId> for YouTube
	Duration float64 // seconds, 0 when unknown
}

// jwCollection is a music section of jw.org/es/biblioteca/música-canciones.
type jwCollection struct {
	Key     string
	Aliases []string // folded, most specific first
}

// jwCollections is ordered so specific names match before generic ones.
var jwCollections = []jwCollection{
	{"SJJMeetings", []string{"cantemos con gozo para las reuniones", "canciones de la reunion", "canticos de la reunion", "para las reuniones"}},
	{"SJJInstrumental", []string{"cantemos con gozo instrumental", "instrumental"}},
	{"SJJChorus", []string{"cantemos con gozo cantadas", "cantemos con gozo", "canticos cantados", "canticos"}},
	{"Vocal", []string{"cantemos a jehova", "coro y orquesta"}},
	{"AudioInternationalMusic", []string{"musica internacional", "internacional"}},
	{"AudioOriginalSongs", []string{"canciones originales", "originales"}},
	{"AudioChildrenSongs", []string{"canciones para ninos", "ninos", "infantiles"}},
	{"KingdomMelodies", []string{"melodias del reino", "melodias"}},
}

// MatchJWCollection finds the mediator key for a spoken section name.
func MatchJWCollection(name string) (string, bool) {
	f := fold(name)
	if f == "" {
		return "", false
	}
	for _, c := range jwCollections {
		for _, a := range c.Aliases {
			if strings.Contains(f, a) || f == a {
				return c.Key, true
			}
		}
	}
	return "", false
}

func (j *JW) Collection(ctx context.Context, key string) ([]Track, string, error) {
	b, err := j.get(ctx, fmt.Sprintf("%s/categories/S/%s?detailed=1&clientType=www", j.Mediator, key))
	if err != nil {
		return nil, "", err
	}
	var r struct {
		Category struct {
			Name  string `json:"name"`
			Media []struct {
				Title    string  `json:"title"`
				Duration float64 `json:"duration"`
				Files    []struct {
					Mime string `json:"mimetype"`
					URL  string `json:"progressiveDownloadURL"`
				} `json:"files"`
			} `json:"media"`
		} `json:"category"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, "", err
	}
	var out []Track
	for _, m := range r.Category.Media {
		for _, f := range m.Files {
			if f.Mime == "audio/mpeg" && f.URL != "" {
				out = append(out, Track{Title: m.Title, URL: f.URL, Duration: m.Duration})
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, "", errors.New("collection has no audio")
	}
	return out, r.Category.Name, nil
}
