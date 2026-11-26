package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Official recordings from jw.org's pub-media API, used instead of the
// device voice when they exist. Markers give where each paragraph
// (mepsParagraphId = the wol data-pid) or Bible verse starts.

type AudioMarker struct {
	Start, Dur float64
	Para       int // wol data-pid
	Verse      int
}

type OfficialAudio struct {
	Title    string
	URL      string
	Duration float64
	Markers  []AudioMarker
}

// parseMediaTime reads "00:01:15.250" as 75.25 seconds; 0 when malformed.
func parseMediaTime(s string) float64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	sec, e3 := strconv.ParseFloat(parts[2], 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0
	}
	return float64(h*3600+m*60) + sec
}

func (j *JW) pubAudio(ctx context.Context, q url.Values) (OfficialAudio, error) {
	if j.PubMedia == "" {
		return OfficialAudio{}, errors.New("no pub-media endpoint")
	}
	q.Set("output", "json")
	q.Set("langwritten", "S")
	q.Set("fileformat", "MP3")
	q.Set("alllangs", "0")
	b, err := j.get(ctx, j.PubMedia+"?"+q.Encode())
	if err != nil {
		return OfficialAudio{}, err
	}
	var r struct {
		Files map[string]map[string][]struct {
			Title    string  `json:"title"`
			Duration float64 `json:"duration"`
			File     struct {
				URL string `json:"url"`
			} `json:"file"`
			Markers *struct {
				Markers []struct {
					Start string `json:"startTime"`
					Dur   string `json:"duration"`
					Para  int    `json:"mepsParagraphId"`
					Verse int    `json:"verseNumber"`
				} `json:"markers"`
			} `json:"markers"`
		} `json:"files"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return OfficialAudio{}, err
	}
	files := r.Files["S"]["MP3"]
	if len(files) == 0 || files[0].File.URL == "" {
		return OfficialAudio{}, errors.New("no official audio")
	}
	f := files[0]
	a := OfficialAudio{Title: f.Title, URL: f.File.URL, Duration: f.Duration}
	if f.Markers != nil {
		for _, m := range f.Markers.Markers {
			a.Markers = append(a.Markers, AudioMarker{Start: parseMediaTime(m.Start), Dur: parseMediaTime(m.Dur), Para: m.Para, Verse: m.Verse})
		}
	}
	return a, nil
}

// AudioByDoc returns the recording of a wol document (study article,
// workbook week, book chapter).
func (j *JW) AudioByDoc(ctx context.Context, docID string) (OfficialAudio, error) {
	return j.pubAudio(ctx, url.Values{"docid": {docID}})
}

// BibleAudio returns the recording of a Bible chapter.
func (j *JW) BibleAudio(ctx context.Context, book, chapter int) (OfficialAudio, error) {
	return j.pubAudio(ctx, url.Values{"pub": {"nwt"}, "booknum": {strconv.Itoa(book)}, "track": {strconv.Itoa(chapter)}})
}

func secs(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// withFragment adds a media fragment "#t=start[,end]" (seconds).
func withFragment(u string, start, end float64) string {
	switch {
	case end > 0:
		return u + "#t=" + secs(start) + "," + secs(end)
	case start > 0:
		return u + "#t=" + secs(start)
	}
	return u
}

// splitMediaFragment undoes withFragment.
func splitMediaFragment(u string) (string, float64, float64) {
	base, frag, ok := strings.Cut(u, "#t=")
	if !ok {
		return u, 0, 0
	}
	a, b, _ := strings.Cut(frag, ",")
	start, _ := strconv.ParseFloat(a, 64)
	end, _ := strconv.ParseFloat(b, 64)
	return base, start, end
}

var reDocID = regexp.MustCompile(`/es/wol/d/r4/lp-s/(\d+)`)

// questionPids maps each paragraph number of a study article to the data-pid
// of its question, where the recording says "Párrafo N. Pregunta".
func questionPids(page string) map[int]int {
	out := map[int]int{}
	for _, m := range reElem.FindAllStringSubmatch(articleBody(page), -1) {
		if m[1] != "p" || !strings.Contains(elemClass(m[2]), "qu") {
			continue
		}
		lead := reLeadNums.FindStringSubmatch(cleanHTML(m[3]))
		pm := regexp.MustCompile(`data-pid="(\d+)"`).FindStringSubmatch(m[2])
		if lead == nil || pm == nil {
			continue
		}
		pid, _ := strconv.Atoi(pm[1])
		for _, n := range numsOf(lead[1]) {
			out[n] = pid
		}
	}
	return out
}

// lastAudio is the recording last started, for "lee el párrafo N".
type audioState struct {
	Name, Title, URL string
	ParaStart        map[int]float64
}

func (a *audioState) jump(n int) (string, bool) {
	if a == nil {
		return "", false
	}
	start, ok := a.ParaStart[n]
	if !ok {
		return "", false
	}
	return withFragment(a.URL, start, 0), true
}

func studyAudioState(name string, page string, a OfficialAudio) *audioState {
	st := &audioState{Name: name, URL: a.URL, ParaStart: map[int]float64{}}
	if r, err := ParseStudyArticle(page); err == nil {
		st.Title = r.Title
	}
	starts := map[int]float64{}
	for _, m := range a.Markers {
		if m.Para > 0 {
			starts[m.Para] = m.Start
		}
	}
	for n, pid := range questionPids(page) {
		if s, ok := starts[pid]; ok {
			st.ParaStart[n] = s
		}
	}
	return st
}

// verseSpan returns where verses from..to start and end in a chapter recording.
func verseSpan(a OfficialAudio, from, to int) (float64, float64, bool) {
	var start, end float64
	found := 0
	for _, m := range a.Markers {
		if m.Verse == from {
			start = m.Start
			found++
		}
		if m.Verse == to {
			end = m.Start + m.Dur
			found++
		}
	}
	if from == to && found == 2 {
		return start, end, true
	}
	return start, end, found == 2
}

func bibleTitle(book, chapter, from, to int) string {
	t := fmt.Sprintf("%s %d", bibleOrder[book-1].Name, chapter)
	switch {
	case from > 0 && from == to:
		t += fmt.Sprintf(", versículo %d", from)
	case from > 0:
		t += fmt.Sprintf(", versículos %d al %d", from, to)
	}
	return t
}
