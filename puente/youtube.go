package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// YouTube talks to the innertube API the YouTube apps use (no API key, no
// login). Search and artist lists use the WEB client, similar songs come from
// the YouTube Music queue (WEB_REMIX), and audio URLs from the IOS_PRODUCER
// client, the only one verified on 2026-11-25 to return plain audio URLs
// without a PO token or JavaScript (research/t6-youtube).
type YouTube struct {
	HTTP  *http.Client
	Web   string // https://www.youtube.com
	Music string // https://music.youtube.com
}

type ytClient struct {
	obj map[string]any
	num string
	ua  string
}

var (
	ytWeb = ytClient{map[string]any{"clientName": "WEB", "clientVersion": "2.20260708.00.00", "hl": "es", "gl": "US"},
		"1", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"}
	ytMusic = ytClient{map[string]any{"clientName": "WEB_REMIX", "clientVersion": "1.20260707.12.00", "hl": "es", "gl": "US"},
		"67", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"}
	ytProducer = ytClient{map[string]any{"clientName": "IOS_PRODUCER", "clientVersion": "0.147.945", "deviceMake": "Apple",
		"deviceModel": "iPhone16,2", "osName": "iPhone", "osVersion": "18.3.2.22D82", "hl": "es", "gl": "US"},
		"92", "com.google.ios.youtubeproducer/0.147.945 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)"}
)

// Song length bounds: shorter is a clip, longer is a compilation or a live.
const ytMinSong, ytMaxSong = 60, 600

func (y *YouTube) post(base string, c ytClient, endpoint string, extra map[string]any) (map[string]any, error) {
	payload := map[string]any{"context": map[string]any{"client": c.obj}}
	for k, v := range extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", base+"/youtubei/v1/"+endpoint+"?prettyPrint=false", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("X-YouTube-Client-Name", c.num)
	req.Header.Set("X-YouTube-Client-Version", c.obj["clientVersion"].(string))
	resp, err := y.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("youtube %s: HTTP %d", endpoint, resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("youtube %s: %v", endpoint, err)
	}
	return out, nil
}

// Search returns song-length videos for a free-text query.
func (y *YouTube) Search(query string) ([]Track, error) {
	resp, err := y.post(y.Web, ytWeb, "search", map[string]any{"query": strings.TrimSpace(query), "params": "EgIQAQ%3D%3D"})
	if err != nil {
		return nil, err
	}
	var out []Track
	walkJSON(resp, func(m map[string]any) {
		vr, ok := m["videoRenderer"].(map[string]any)
		if !ok {
			return
		}
		tr := ytTrack(vr, ytText(vr["ownerText"]))
		if tr.URL != "" && tr.Duration >= ytMinSong && tr.Duration <= ytMaxSong {
			out = append(out, tr)
		}
	})
	return out, nil
}

// Similar returns the YouTube Music queue started from a song: the song
// itself first, then songs of similar artists.
func (y *YouTube) Similar(videoID string) ([]Track, error) {
	return y.mix(y.Music, ytMusic, videoID)
}

// Artist returns songs by one artist: the YouTube mix of their first search
// hit (which stays mostly on that artist) plus the search hits, keeping only
// tracks whose channel or byline names the artist.
func (y *YouTube) Artist(name string) ([]Track, error) {
	want := fold(name)
	hits, err := y.Search(name)
	if err != nil {
		return nil, err
	}
	var mine []Track
	for _, h := range hits {
		if similarName(want, fold(h.Artist)) {
			mine = append(mine, h)
		}
	}
	if len(mine) == 0 {
		return nil, fmt.Errorf("no songs found for artist %q", name)
	}
	mix, err := y.mix(y.Web, ytWeb, strings.TrimPrefix(mine[0].URL, "yt:"))
	if err != nil {
		logf("youtube artist mix: %v", err)
	}
	seen := map[string]bool{}
	var out []Track
	for _, tr := range append(mix, mine...) {
		if seen[tr.URL] || !similarName(want, fold(tr.Artist)) {
			continue
		}
		seen[tr.URL] = true
		out = append(out, tr)
	}
	return out, nil
}

func (y *YouTube) mix(base string, c ytClient, videoID string) ([]Track, error) {
	resp, err := y.post(base, c, "next", map[string]any{"videoId": videoID, "playlistId": "RD" + videoID})
	if err != nil {
		return nil, err
	}
	var out []Track
	walkJSON(resp, func(m map[string]any) {
		vr, ok := m["playlistPanelVideoRenderer"].(map[string]any)
		if !ok {
			return
		}
		artist := ytFirstRun(vr["shortBylineText"])
		if artist == "" {
			artist = ytFirstRun(vr["longBylineText"])
		}
		if tr := ytTrack(vr, artist); tr.URL != "" {
			out = append(out, tr)
		}
	})
	if len(out) == 0 {
		return nil, errors.New("youtube mix: no songs")
	}
	return out, nil
}

// AudioURL returns a direct AAC (itag 140, m4a) URL for a video.
func (y *YouTube) AudioURL(videoID string) (string, error) {
	resp, err := y.post(y.Web, ytProducer, "player", map[string]any{"videoId": videoID, "contentCheckOk": true, "racyCheckOk": true})
	if err != nil {
		return "", err
	}
	ps, _ := resp["playabilityStatus"].(map[string]any)
	if st, _ := ps["status"].(string); st != "OK" {
		reason, _ := ps["reason"].(string)
		return "", fmt.Errorf("youtube player: %s %s", st, reason)
	}
	sd, _ := resp["streamingData"].(map[string]any)
	formats, _ := sd["adaptiveFormats"].([]any)
	fallback := ""
	for _, f := range formats {
		fm, _ := f.(map[string]any)
		u, _ := fm["url"].(string)
		mime, _ := fm["mimeType"].(string)
		if u == "" || !strings.HasPrefix(mime, "audio/mp4") {
			continue
		}
		if itag, _ := fm["itag"].(float64); itag == 140 {
			return u, nil
		}
		if fallback == "" {
			fallback = u
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("youtube player: no AAC audio format with a plain URL")
}

func ytTrack(vr map[string]any, artist string) Track {
	id, _ := vr["videoId"].(string)
	if id == "" {
		return Track{}
	}
	return Track{Title: ytText(vr["title"]), Artist: artist, URL: "yt:" + id, Duration: float64(ytDuration(ytText(vr["lengthText"])))}
}

// walkJSON calls fn on every object of a decoded JSON tree, depth first in
// document order.
func walkJSON(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		fn(t)
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkJSON(t[k], fn)
		}
	case []any:
		for _, e := range t {
			walkJSON(e, fn)
		}
	}
}

// ytText joins the runs of an innertube text object, or returns simpleText.
func ytText(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m["simpleText"].(string); ok {
		return s
	}
	var b strings.Builder
	runs, _ := m["runs"].([]any)
	for _, r := range runs {
		if rm, ok := r.(map[string]any); ok {
			s, _ := rm["text"].(string)
			b.WriteString(s)
		}
	}
	return b.String()
}

func ytFirstRun(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if runs, ok := m["runs"].([]any); ok && len(runs) > 0 {
		if rm, ok := runs[0].(map[string]any); ok {
			s, _ := rm["text"].(string)
			return s
		}
	}
	s, _ := m["simpleText"].(string)
	return s
}

// ytDuration parses "3:45" or "1:05:00" into seconds; 0 when unknown.
func ytDuration(s string) int {
	if s == "" {
		return 0
	}
	total := 0
	for _, p := range strings.Split(strings.TrimSpace(s), ":") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

// rangeReader reads a remote file as an io.ReadSeeker with Range requests
// of Chunk bytes. Plain GETs of googlevideo URLs are throttled to ~32 KB/s
// while ranged ones run at MB/s, so every request carries a Range header.
type rangeReader struct {
	HTTP  *http.Client
	URL   string
	Chunk int64
	Ctx   context.Context

	size   int64 // -1 until the first response
	sized  bool
	off    int64
	buf    []byte
	bufOff int64
}

func (r *rangeReader) ctx() context.Context {
	if r.Ctx != nil {
		return r.Ctx
	}
	return context.Background()
}

// fetch loads the chunk that starts at off into buf.
func (r *rangeReader) fetch(off int64) error {
	chunk := r.Chunk
	if chunk <= 0 {
		chunk = 1 << 20
	}
	req, err := http.NewRequestWithContext(r.ctx(), "GET", r.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+chunk-1))
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndexByte(cr, '/'); i >= 0 {
				if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					r.size, r.sized = n, true
				}
			}
		}
	case http.StatusRequestedRangeNotSatisfiable:
		r.buf, r.bufOff = nil, off
		return io.EOF
	case http.StatusOK:
		// Server ignored the range: skip to off and keep one chunk.
		if resp.ContentLength >= 0 {
			r.size, r.sized = resp.ContentLength, true
		}
		if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
			return err
		}
	default:
		return fmt.Errorf("range GET: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, chunk))
	if err != nil {
		return err
	}
	r.buf, r.bufOff = b, off
	return nil
}

func (r *rangeReader) Read(p []byte) (int, error) {
	if r.sized && r.off >= r.size {
		return 0, io.EOF
	}
	if r.off < r.bufOff || r.off >= r.bufOff+int64(len(r.buf)) {
		if err := r.fetch(r.off); err != nil {
			return 0, err
		}
		if len(r.buf) == 0 {
			return 0, io.EOF
		}
	}
	n := copy(p, r.buf[r.off-r.bufOff:])
	r.off += int64(n)
	return n, nil
}

func (r *rangeReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.off
	case io.SeekEnd:
		if !r.sized {
			if err := r.fetch(0); err != nil && err != io.EOF {
				return 0, err
			}
			if !r.sized {
				return 0, errors.New("range reader: unknown size")
			}
		}
		offset += r.size
	default:
		return 0, errors.New("range reader: bad whence")
	}
	if offset < 0 {
		return 0, errors.New("range reader: negative position")
	}
	r.off = offset
	return offset, nil
}

// similarName reports whether a spoken artist name (folded) names the
// artist: each spoken word must match a word of the artist's name with at
// most one wrong letter (two in words of five letters or more), because
// speech recognition spells names as they sound ("Mark Anthony").
func similarName(want, artist string) bool {
	if want == "" {
		return false
	}
	if strings.Contains(artist, want) {
		return true
	}
	words := strings.Fields(artist)
	for _, w := range strings.Fields(want) {
		allowed := 1
		if len([]rune(w)) >= 5 {
			allowed = 2
		}
		ok := false
		for _, a := range words {
			if editDistance(w, a) <= allowed {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
