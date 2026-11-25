// ytspike: feasibility spike for playing YouTube music audio from a small Go
// program, using the live innertube API the same way yt-dlp does.
//
// Subcommands (all output JSON):
//
//	ytspike search "query"      -> first 10 music video results
//	ytspike mix <videoId>       -> YouTube + YouTube Music mix (radio) lists
//	ytspike audio <videoId>     -> per-client playability / formats / range GET
//	                               + optional full itag-140 download speed test
//
// Standard library only. No cookies, no JS, no PO tokens.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Client identity table (mirrors yt-dlp INNERTUBE_CLIENTS, see ./yt-dlp-base.py)
// ---------------------------------------------------------------------------

type ClientDef struct {
	Name      string
	Host      string // innertube host
	ClientObj map[string]any
	ClientNum int
	UA        string
}

func clientDef(name string, num int, clientObj map[string]any, ua, host string) ClientDef {
	return ClientDef{Name: name, Host: host, ClientObj: clientObj, ClientNum: num, UA: ua}
}

var clients = map[string]ClientDef{
	// android_vr: yt-dlp notes version 1.65.10 got 403 for all formats since
	// 2026.08.17, but we test empirically rather than assume.
	"android_vr": clientDef("android_vr", 28, map[string]any{
		"clientName":        "ANDROID_VR",
		"clientVersion":     "1.65.10",
		"deviceMake":        "Oculus",
		"deviceModel":       "Quest 3",
		"androidSdkVersion": 32,
		"osName":            "Android",
		"osVersion":         "12L",
	}, "com.google.android.apps.youtube.vr.oculus/1.65.10 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip", "www.youtube.com"),
	"ios": clientDef("ios", 5, map[string]any{
		"clientName":    "IOS",
		"clientVersion": "21.26.4",
		"deviceMake":    "Apple",
		"deviceModel":   "iPhone16,2",
		"osName":        "iPhone",
		"osVersion":     "18.3.2.22D82",
	}, "com.google.ios.youtube/21.26.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)", "www.youtube.com"),
	"android": clientDef("android", 3, map[string]any{
		"clientName":        "ANDROID",
		"clientVersion":     "21.26.364",
		"androidSdkVersion": 30,
		"osName":            "Android",
		"osVersion":         "11",
	}, "com.google.android.youtube/21.26.364 (Linux; U; Android 11) gzip", "www.youtube.com"),
	"tv": clientDef("tv", 7, map[string]any{
		"clientName":    "TVHTML5",
		"clientVersion": "7.20260707.07.00",
	}, "Mozilla/5.0 (ChromiumStylePlatform) Cobalt/25.lts.30.1034943-gold (unlike Gecko), Unknown_TV_Unknown_0/Unknown (Unknown, Unknown)", "www.youtube.com"),
	"tv_samsung": clientDef("tv_samsung", 7, map[string]any{
		"clientName":    "TVHTML5",
		"clientVersion": "5.20260707",
		"deviceMake":    "Samsung",
		"deviceModel":   "UKS9800",
		"osName":        "Tizen",
		"osVersion":     "2.4.0",
	}, "Mozilla/5.0 (SMART-TV; Linux; Tizen 2.4.0) AppleWebKit/538.1 (KHTML, like Gecko) Version/2.4.0 TV Safari/538.1", "www.youtube.com"),
	"visionos": clientDef("visionos", 101, map[string]any{
		"clientName":    "VISIONOS",
		"clientVersion": "1.02",
		"deviceMake":    "Apple",
		"deviceModel":   "RealityDevice17,1",
		"osName":        "visionOS",
		"osVersion":     "26.5.23O471",
	}, "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15", "www.youtube.com"),
	"ios_producer": clientDef("ios_producer", 92, map[string]any{
		"clientName":    "IOS_PRODUCER",
		"clientVersion": "0.147.945",
		"deviceMake":    "Apple",
		"deviceModel":   "iPhone16,2",
		"osName":        "iPhone",
		"osVersion":     "18.3.2.22D82",
	}, "com.google.ios.youtubeproducer/0.147.945 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)", "www.youtube.com"),
}

var clientOrder = []string{"android_vr", "ios", "android", "tv", "tv_samsung", "visionos", "ios_producer"}

// webMusic is the WEB_REMIX identity used against music.youtube.com.
var webMusic = ClientDef{
	Name: "web_music",
	Host: "music.youtube.com",
	ClientObj: map[string]any{
		"clientName":    "WEB_REMIX",
		"clientVersion": "1.20260707.12.00",
	},
	ClientNum: 67,
	UA:        "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
}

var webClient = ClientDef{
	Name: "web",
	Host: "www.youtube.com",
	ClientObj: map[string]any{
		"clientName":    "WEB",
		"clientVersion": "2.20260708.00.00",
	},
	ClientNum: 1,
	UA:        "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// ---------------------------------------------------------------------------
// innertube plumbing
// ---------------------------------------------------------------------------

func postInnertube(c ClientDef, endpoint string, extra map[string]any) (map[string]any, int, error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": c.ClientObj,
		},
	}
	for k, v := range extra {
		payload[k] = v
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	reqURL := fmt.Sprintf("https://%s/youtubei/v1/%s?prettyPrint=false", c.Host, endpoint)
	req, err := http.NewRequest(http.MethodPost, reqURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("X-YouTube-Client-Name", strconv.Itoa(c.ClientNum))
	req.Header.Set("X-YouTube-Client-Version", anyToString(c.ClientObj["clientVersion"]))
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("bad JSON (%d bytes): %v; head=%s", len(raw), err, truncate(string(raw), 200))
	}
	return parsed, resp.StatusCode, nil
}

func anyToString(v any) string {
	s, _ := v.(string)
	return s
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func getMap(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func getList(m map[string]any, keys ...string) []any {
	cur := m
	for i, k := range keys {
		if i == len(keys)-1 {
			list, _ := cur[k].([]any)
			return list
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return nil
}

func getStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// textOf collapses {runs:[{text:...}]} or {simpleText} or a bare string.
func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["simpleText"].(string); ok {
			return s
		}
		var out []string
		for _, r := range getList(t, "runs") {
			if rm, ok := r.(map[string]any); ok {
				out = append(out, getStr(rm, "text"))
			}
		}
		return strings.Join(out, "")
	}
	return ""
}

func parseDuration(text string) any {
	// "4:03" -> 243 ; live / upcoming -> null
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) == 0 {
		return nil
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil
		}
		total = total*60 + n
	}
	return total
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

type SearchResult struct {
	VideoID  string   `json:"videoId"`
	Title    string   `json:"title"`
	Artist   string   `json:"artist"`
	Duration *int     `json:"durationSeconds"`
	DurationRaw string `json:"durationRaw,omitempty"`
}

func runSearch(query string, music bool) (any, error) {
	c := webClient
	if music {
		c = webMusic
	}
	extra := map[string]any{"query": strings.TrimSpace(query), "params": "EgIQAQ%3D%3D"}
	resp, _, err := postInnertube(c, "search", extra)
	if err != nil {
		// params encoding sometimes upsets music; retry without filter
		if music {
			extra = map[string]any{"query": strings.TrimSpace(query)}
			resp, _, err = postInnertube(c, "search", extra)
		}
		if err != nil {
			return nil, err
		}
	}

	results := []SearchResult{}
	var walk func(node any)
	var visitRenderer func(r map[string]any)
	visitRenderer = func(r map[string]any) {
		if vr, ok := r["videoRenderer"].(map[string]any); ok {
			id := getStr(vr, "videoId")
			if id == "" {
				return
			}
			res := SearchResult{VideoID: id, Title: textOf(vr["title"])}
			res.Artist = textOf(vr["ownerText"])
			if res.Artist == "" {
				res.Artist = textOf(vr["longBylineText"])
			}
			if res.Artist == "" {
				res.Artist = textOf(vr["shortBylineText"])
			}
			raw := textOf(vr["lengthText"])
			res.DurationRaw = raw
			if d, ok := parseDuration(raw).(int); ok {
				res.Duration = &d
			} else {
				res.Duration = nil
			}
			results = append(results, res)
		}
	}
	walk = func(node any) {
		switch t := node.(type) {
		case map[string]any:
			visitRenderer(t)
			for _, v := range t {
				walk(v)
			}
		case []any:
			for _, v := range t {
				walk(v)
			}
		}
	}
	walk(resp)
	if len(results) > 10 {
		results = results[:10]
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// mix (radio) via `next`
// ---------------------------------------------------------------------------

type MixItem struct {
	VideoID string `json:"videoId"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
}

func runNext(c ClientDef, videoID string) (any, error) {
	extra := map[string]any{
		"videoId":    videoID,
		"playlistId": "RD" + videoID,
	}
	resp, _, err := postInnertube(c, "next", extra)
	if err != nil {
		return nil, err
	}
	items := []MixItem{}
	addItem := func(vr map[string]any) {
		id := getStr(vr, "videoId")
		if id == "" {
			return
		}
		// Artist: first run of short/long byline (music payloads decorate the
		// long byline with „/ album • year/”, so take the first run only).
		artist := firstRunText(vr["shortBylineText"])
		if artist == "" {
			artist = firstRunText(vr["longBylineText"])
		}
		items = append(items, MixItem{VideoID: id, Title: textOf(vr["title"]), Artist: artist})
	}
	if pl := getMap(resp, "contents", "twoColumnWatchNextResults", "playlist", "playlist"); pl != nil {
		for _, it := range getList(pl, "contents") {
			if pan, ok := it.(map[string]any); ok {
				if vr, ok := pan["playlistPanelVideoRenderer"].(map[string]any); ok {
					addItem(vr)
				}
			}
		}
	}
	if tabsPayload := getMap(resp, "contents", "singleColumnMusicWatchNextResultsRenderer", "tabbedRenderer", "watchNextTabbedResultsRenderer"); tabsPayload != nil {
		for _, tabI := range getList(tabsPayload, "tabs") {
			tab, ok := tabI.(map[string]any)
			if !ok {
				continue
			}
			mr := getMap(tab, "tabRenderer", "content", "musicQueueRenderer", "content", "playlistPanelRenderer")
			if mr == nil {
				continue
			}
			for _, it := range getList(mr, "contents") {
				if pan, ok := it.(map[string]any); ok {
					if vr, ok := pan["playlistPanelVideoRenderer"].(map[string]any); ok {
						addItem(vr)
					}
				}
			}
		}
	}
	return items, nil
}

// firstRunText returns the text of the first run, or simpleText.
func firstRunText(v any) string {
	if m, ok := v.(map[string]any); ok {
		if runs, ok := m["runs"].([]any); ok && len(runs) > 0 {
			if rm, ok := runs[0].(map[string]any); ok {
				return getStr(rm, "text")
			}
		}
		if s, ok := m["simpleText"].(string); ok {
			return s
		}
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type MixReport struct {
	Source         string    `json:"source"`
	Host           string    `json:"host"`
	Count          int       `json:"count"`
	SameArtistHits int       `json:"same_artist_hits"`
	DistinctArtist int       `json:"distinct_artists"`
	Items          []MixItem `json:"items"`
}

// ---------------------------------------------------------------------------
// player / audio
// ---------------------------------------------------------------------------

type AudioFormat struct {
	Itag          int     `json:"itag"`
	MimeType      string  `json:"mimeType"`
	Bitrate       int     `json:"bitrate"`
	ContentLength int64   `json:"contentLength"`
	AudioQuality  string  `json:"audioQuality"`
	URL           string  `json:"url"`
	HasPlainURL   bool    `json:"-"`
	SignatureCipher string `json:"signatureCipher,omitempty"`
}

type ClientReport struct {
	Name          string   `json:"name"`
	HTTPStatus    int      `json:"http_status"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason"`
	AudioItags    []int    `json:"audio_itags"`
	PlainURL      bool     `json:"plain_url"`
	RangeHTTP     *int     `json:"range_get_http"`
	RangeBytes    int64    `json:"range_bytes"`
	Speex         string   `json:"notes"`
	Error         string   `json:"error,omitempty"`
	AudioFormats []AudioFormat `json:"audio_formats,omitempty"`
}

func runPlayer(c ClientDef, videoID string) (string, string, []AudioFormat, map[string]any, error) {
	extra := map[string]any{
		"videoId":        videoID,
		"contentCheckOk": true,
		"racyCheckOk":    true,
	}
	resp, _, err := postInnertube(c, "player", extra)
	if err != nil {
		return "", "", nil, nil, err
	}
	ps := getMap(resp, "playabilityStatus")
	status := getStr(ps, "status")
	reason := getStr(ps, "reason")

	audio := []AudioFormat{}
	sd := getMap(resp, "streamingData")
	if sd != nil {
		for _, key := range []string{"formats", "adaptiveFormats"} {
			for _, f := range getList(sd, key) {
				fm, ok := f.(map[string]any)
				if !ok {
					continue
				}
				mime := getStr(fm, "mimeType")
				if !strings.Contains(mime, "audio/") {
					continue
				}
				itag, _ := fm["itag"].(float64)
				bitrate, _ := fm["bitrate"].(float64)
				clen, _ := fm["contentLength"].(float64)
				u := getStr(fm, "url")
				af := AudioFormat{
					Itag:          int(itag),
					MimeType:      mime,
					Bitrate:       int(bitrate),
					ContentLength: int64(clen),
					AudioQuality:  getStr(fm, "audioQuality"),
					URL:           u,
					HasPlainURL:   u != "",
					SignatureCipher: getStr(fm, "signatureCipher"),
				}
				audio = append(audio, af)
			}
		}
		sort.Slice(audio, func(i, j int) bool { return audio[i].Bitrate > audio[j].Bitrate })
	}
	errReason := getStr(getMap(resp, "playabilityStatus"), "errorScreen@style")
	_ = errReason
	return status, reason, audio, resp, nil
}

// rangeGet appends range=0-262143 and returns http status and byte count.
func rangeGet(format AudioFormat, ua string) (int, int64, string, error) {
	reqURL := format.URL + "&range=0-262143"
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, 0, "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", "bytes=0-262143")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	body := resp.Status
	return resp.StatusCode, n, body, nil
}

func pickPreferred(audio []AudioFormat) *AudioFormat {
	for _, itag := range []int{140, 251, 249, 250} {
		for i := range audio {
			if audio[i].Itag == itag && audio[i].HasPlainURL {
				return &audio[i]
			}
		}
	}
	return nil
}

func runAudio(videoID string, wantNames []string, speedItag int, keep bool) any {
	reports := []ClientReport{}
	for _, name := range wantNames {
		c, ok := clients[name]
		if !ok {
			continue
		}
		rep := ClientReport{Name: c.Name, AudioItags: []int{}}
		status, reason, audio, _, err := runPlayer(c, videoID)
		rep.Status = status
		rep.Reason = reason
		if err != nil {
			rep.Error = err.Error()
		}
		sort.Slice(audio, func(i, j int) bool { return audio[i].Itag < audio[j].Itag })
		rep.AudioFormats = audio
		for _, a := range audio {
			rep.AudioItags = append(rep.AudioItags, a.Itag)
			if a.HasPlainURL {
				rep.PlainURL = true
			}
		}
		if pref := pickPreferred(audio); pref != nil {
			code, nbytes, _, rerr := rangeGet(*pref, c.UA)
			rep.RangeHTTP = &code
			rep.RangeBytes = nbytes
			if rerr != nil {
				rep.Speex = "range GET error: " + rerr.Error()
			} else if code != 200 && code != 206 {
				rep.Speex = fmt.Sprintf("range GET returned %d (expected 206/200)", code)
			} else if nbytes < 131072 {
				rep.Speex = fmt.Sprintf("range GET too small: %d bytes", nbytes)
			} else {
				rep.Speex = "range GET OK"
			}
			if speedItag > 0 && pref.Itag == speedItag {
				speed, err := measureDownloadSpeed(*pref, c.UA, videoID, keep)
				if err != nil {
					rep.Speex += "; speed test failed: " + err.Error()
				} else {
					rep.Speex += fmt.Sprintf("; full itag %d download speed: %.0f bytes/s", speedItag, speed)
				}
			}
		} else if len(audio) > 0 {
			rep.Speex = "no preferred audio itag with a plain url (signature solving would be required)"
		} else {
			rep.Speex = "no audio formats in response"
		}
		reports = append(reports, rep)
	}
	return reports
}

func measureDownloadSpeed(f AudioFormat, ua, videoID string, keep bool) (float64, error) {
	req, err := http.NewRequest(http.MethodGet, f.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", ua)
	// Streaming always requests ranges; a plain headerless GET is throttled
	// server-wide to ~32 KB/s (measured), while Range requests run at MB/s.
	req.Header.Set("Range", "bytes=0-")
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return 0, fmt.Errorf("http %d on full download", resp.StatusCode)
	}
	fname := fmt.Sprintf("audio_test_itag%d_%s.bin", f.Itag, videoID)
	out, err := os.Create(fname)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, resp.Body)
	dt := time.Since(start).Seconds()
	out.Close()
	if !keep {
		os.Remove(fname)
	}
	if err != nil {
		return float64(n) / dt, nil // still report the speed we achieved
	}
	if dt <= 0 {
		return 0, errors.New("zero elapsed time")
	}
	return float64(n) / dt, nil
}

// ---------------------------------------------------------------------------
// main dispatch
// ---------------------------------------------------------------------------

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ytspike search \"q\" | mix <id> | audio <id>")
		os.Exit(2)
	}
	cmd := os.Args[1]
	switch cmd {
	case "search":
		fs := flag.NewFlagSet("search", flag.ExitOnError)
		music := fs.Bool("music", false, "search via YouTube Music (WEB_REMIX)")
		fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			fail("search: missing query")
		}
		out, err := runSearch(fs.Arg(0), *music)
		emit(out, err)
	case "mix":
		fs := flag.NewFlagSet("mix", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			fail("mix: missing videoId")
		}
		id := fs.Arg(0)
		out := map[string]any{}
		ytItems, ytErr := runNext(webClient, id)
		mixItems := ytItems
		if mixItems == nil {
			mixItems = []MixItem{}
		}
		rep := MixReport{Source: "youtube", Host: webClient.Host, Count: len(mixItems.([]MixItem))}
		if ytErr != nil {
			rep.Count = 0
			out["youtube_error"] = ytErr.Error()
		}
		rep.Items = mixItems.([]MixItem)
		rep.SameArtistHits, rep.DistinctArtist = mixStats(rep.Items)
		out["youtube"] = rep

		mItems, mErr := runNext(webMusic, id)
		mix := []MixItem{}
		if mItems != nil {
			mix = mItems.([]MixItem)
		}
		// WEB_REMIX next usually needs things like playlistId but might return
		// mic/search panels; capture raw on failure.
		mrep := MixReport{Source: "youtube_music", Host: webMusic.Host, Count: len(mix)}
		mrep.Items = mix
		mrep.SameArtistHits, mrep.DistinctArtist = mixStats(mix)
		if mErr != nil {
			out["youtube_music_error"] = mErr.Error()
		}
		out["youtube_music"] = mrep

		// Heuristic: same_artist_hits prefers a radio seeded with the artist.
		better := "null"
		yrep, _ := out["youtube"].(MixReport)
		if mErr == nil && ytErr == nil {
			switch {
			case mrep.Count >= 15 && mrep.SameArtistHits > yrep.SameArtistHits:
				better = "youtube_music"
			case yrep.Count >= 15:
				better = "youtube"
			case mrep.Count >= 15:
				better = "youtube_music"
			}
		}
		out["mix_best_source"] = better
		emit(out, nil)
	case "audio":
		fs := flag.NewFlagSet("audio", flag.ExitOnError)
		clist := fs.String("clients", strings.Join(clientOrder, ","), "comma separated clients")
		speed := fs.Int("speed-itag", 0, "download this itag fully and measure bytes/s")
		keep := fs.Bool("keep", false, "keep the downloaded file (debug)")
		fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			fail("audio: missing videoId")
		}
		out := runAudio(fs.Arg(0), strings.Split(*clist, ","), *speed, *keep)
		emit(out, nil)
	case "debug":
	// debug <endpoint> <videoId|query> <client>  -> raw innertube response
	fs := flag.NewFlagSet("debug", flag.ExitOnError)
	fs.Parse(os.Args[2:])
	if fs.NArg() < 2 {
		fail("debug: missing endpoint and videoId-or-query; optional third arg = client (default web)")
	}
	endpoint, idOrQuery := fs.Arg(0), fs.Arg(1)
	c := webClient
	if fs.NArg() >= 3 {
		ident := fs.Arg(2)
		if ident == "web_music" {
			c = webMusic
		} else if def, ok := clients[ident]; ok {
			c = def
		} else {
			fail("debug: unknown client " + ident)
		}
	}
	var extra map[string]any
	if endpoint == "search" {
		extra = map[string]any{"query": idOrQuery}
	} else if endpoint == "next" {
		extra = map[string]any{"videoId": idOrQuery, "playlistId": "RD" + idOrQuery}
	} else if endpoint == "player" {
		extra = map[string]any{"videoId": idOrQuery, "contentCheckOk": true, "racyCheckOk": true}
	} else {
		fail("debug: unsupported endpoint " + endpoint)
	}
	resp, code, err := postInnertube(c, endpoint, extra)
	if err != nil {
		emit(map[string]any{"http": code, "error": err.Error()}, nil)
		return
	}
	emit(resp, nil)
case "clients":
		names := make([]string, 0, len(clientOrder))
		for _, n := range clientOrder {
			names = append(names, n)
		}
		emit(names, nil)
	default:
		fail("unknown command: " + cmd)
	}
}

func mixStats(items []MixItem) (int, int) {
	if len(items) == 0 {
		return 0, 0
	}
	seed := items[0].Artist
	same := 0
	distinct := map[string]bool{}
	for _, it := range items[1:] {
		distinct[it.Artist] = true
		if strings.EqualFold(strings.TrimSpace(it.Artist), strings.TrimSpace(seed)) {
			same++
		}
	}
	return same, len(distinct)
}

func emit(out any, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if e := enc.Encode(out); e != nil {
		os.Exit(1)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}

var _ = url.QueryEscape
