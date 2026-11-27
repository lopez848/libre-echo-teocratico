package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// mediatorCategory is a JW Broadcasting category from the mediator API. A
// "container" holds subcategories; an "ondemand" category holds videos.
type mediatorCategory struct {
	Key           string             `json:"key"`
	Name          string             `json:"name"`
	Type          string             `json:"type"`
	Media         []mediatorMedia    `json:"media"`
	Subcategories []mediatorCategory `json:"subcategories"`
}

type mediatorMedia struct {
	Title     string  `json:"title"`
	Published string  `json:"firstPublished"`
	Duration  float64 `json:"duration"`
	Files     []struct {
		Mime string `json:"mimetype"`
		Size int64  `json:"filesize"`
		URL  string `json:"progressiveDownloadURL"`
	} `json:"files"`
}

func (j *JW) category(ctx context.Context, key string) (mediatorCategory, error) {
	b, err := j.get(ctx, fmt.Sprintf("%s/categories/S/%s?detailed=1&clientType=www", j.Mediator, key))
	if err != nil {
		return mediatorCategory{}, err
	}
	var r struct {
		Category mediatorCategory `json:"category"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return mediatorCategory{}, err
	}
	return r.Category, nil
}

type datedTrack struct {
	t   Track
	pub string
}

// tracksOf keeps each video as its smallest MP4 (its AAC audio is played).
func tracksOf(ms []mediatorMedia) []datedTrack {
	var out []datedTrack
	for _, m := range ms {
		best := ""
		var size int64
		for _, f := range m.Files {
			if f.Mime == "video/mp4" && f.URL != "" && (best == "" || f.Size < size) {
				best, size = f.URL, f.Size
			}
		}
		if best != "" {
			out = append(out, datedTrack{Track{Title: m.Title, URL: best, Duration: m.Duration}, m.Published})
		}
	}
	return out
}

// newestFirst sorts by publication date and drops repeated videos (the same
// video can be listed in several subcategories).
func newestFirst(items []datedTrack) []Track {
	sort.SliceStable(items, func(a, b int) bool { return items[a].pub > items[b].pub })
	seen := map[string]bool{}
	var out []Track
	for _, it := range items {
		if !seen[it.t.URL] {
			seen[it.t.URL] = true
			out = append(out, it.t)
		}
	}
	return out
}

// CategoryTracks returns every video of a category and its subcategories
// (two levels at most), newest first.
func (j *JW) CategoryTracks(ctx context.Context, key string) ([]Track, error) {
	var items []datedTrack
	var walk func(key string, depth int) error
	walk = func(key string, depth int) error {
		c, err := j.category(ctx, key)
		if err != nil {
			return err
		}
		items = append(items, tracksOf(c.Media)...)
		for _, sub := range c.Subcategories {
			items = append(items, tracksOf(sub.Media)...)
			if len(sub.Media) == 0 && sub.Type == "container" && depth < 2 {
				if err := walk(sub.Key, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(key, 1); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s: no videos", key)
	}
	return newestFirst(items), nil
}

var reConvention = regexp.MustCompile(`^(\d{4})Convention$`)

// LatestConvention returns the sessions of the newest regional convention
// (its category key changes every year: 2026Convention, ...), in order.
func (j *JW) LatestConvention(ctx context.Context) ([]Track, error) {
	c, err := j.category(ctx, "VODProgramsEvents")
	if err != nil {
		return nil, err
	}
	var best *mediatorCategory
	for i, sub := range c.Subcategories {
		if reConvention.MatchString(sub.Key) && (best == nil || sub.Key > best.Key) {
			best = &c.Subcategories[i]
		}
	}
	if best == nil {
		return nil, errors.New("no regional convention found")
	}
	tracks := newestFirst(tracksOf(best.Media))
	if len(tracks) == 0 {
		return nil, errors.New("convention: no videos")
	}
	return tracks, nil
}

// broadcastingSections are the JW Broadcasting sections Mia can play. The
// productions use the audio-described versions: a narrator tells what
// happens on screen, which suits a speaker.
var broadcastingSections = map[string]struct{ key, label string }{
	"programa_mensual":     {"StudioMonthlyPrograms", "los programas mensuales de JW Broadcasting"},
	"discursos":            {"StudioTalks", "los discursos de JW Broadcasting"},
	"noticias":             {"StudioNewsReports", "las noticias y anuncios de JW Broadcasting"},
	"experiencias":         {"VODIntExp", "las entrevistas y experiencias"},
	"producciones":         {"VODDramasAD", "las producciones audiovisuales con audiodescripción"},
	"ninos":                {"VODChildren", "los videos para niños"},
	"jovenes":              {"VODTeenagers", "los videos para jóvenes"},
	"familia":              {"VODFamily", "los videos para la familia"},
	"galaad":               {"VODPgmEvtGilead", "las graduaciones de Galaad"},
	"reunion_anual":        {"VODPgmEvtAnnMtg", "las reuniones anuales"},
	"programas_especiales": {"VODPgmEvtSpecial", "los programas especiales"},
	"asamblea":             {"", "la asamblea regional más reciente"},
}

func playJWBroadcasting(section, mode string) string {
	if svc.JW == nil || svc.Player == nil {
		return "Todavía no puedo poner JW Broadcasting."
	}
	sec, ok := broadcastingSections[strings.ReplaceAll(fold(strings.ReplaceAll(section, "_", " ")), " ", "_")]
	if !ok {
		return "No conozco esa sección de JW Broadcasting. Puedo poner el programa mensual, discursos, noticias, experiencias, producciones, videos para niños, jóvenes o la familia, la graduación de Galaad, la reunión anual, programas especiales o la asamblea regional."
	}
	ctx, cancel := wolCtx()
	defer cancel()
	var tracks []Track
	var err error
	if sec.key == "" {
		tracks, err = svc.JW.LatestConvention(ctx)
	} else {
		tracks, err = svc.JW.CategoryTracks(ctx, sec.key)
	}
	if err != nil || len(tracks) == 0 {
		logf("jw broadcasting %s: %v", section, err)
		return "No pude traer esos videos de JW Broadcasting."
	}
	if f := fold(mode); strings.Contains(f, "reciente") || strings.Contains(f, "ultim") || strings.Contains(f, "nuev") || strings.Contains(f, "este mes") {
		first, err := startMusic(sec.label, tracks[:1], false)
		if err != nil {
			return failed(err)
		}
		return "Poniendo lo más reciente de " + sec.label + ": " + spoken(first.Title) + "."
	}
	if sec.key == "" { // a convention is heard in order, first session first
		ordered := make([]Track, len(tracks))
		for i, t := range tracks {
			ordered[len(tracks)-1-i] = t
		}
		first, err := startMusic(sec.label, ordered, false)
		if err != nil {
			return failed(err)
		}
		return "Poniendo " + sec.label + ". Empieza " + spoken(first.Title) + "."
	}
	first, err := startMusic(sec.label, tracks, true)
	if err != nil {
		return failed(err)
	}
	return "Poniendo " + sec.label + " en orden aleatorio. Empieza " + spoken(first.Title) + "."
}
