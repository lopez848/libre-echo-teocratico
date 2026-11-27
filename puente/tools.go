package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// API is the subset of Device the tools need (mockable in tests).
type API interface {
	Call(method, path string, payload any, out any) error
}

// toolSpecs is the OpenAI "tools" array sent upstream.
var toolSpecs = json.RawMessage(`[
{"type":"function","function":{"name":"set_timer","description":"Start a countdown timer. Also use it for relative reminders like 'recuérdame en 20 minutos sacar la ropa' (label = what to remember).","parameters":{"type":"object","properties":{"seconds":{"type":"integer","description":"Total duration in seconds"},"label":{"type":"string","description":"Short name in Spanish, e.g. arroz"}},"required":["seconds"]}}},
{"type":"function","function":{"name":"cancel_timers","description":"Cancel countdown timers. With label cancels only matching ones, without label cancels all timers.","parameters":{"type":"object","properties":{"label":{"type":"string"}}}}},
{"type":"function","function":{"name":"list_timers","description":"Tell how much time is left on timers and which alarms and reminders are set.","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"set_alarm","description":"Set an alarm at a clock time.","parameters":{"type":"object","properties":{"time":{"type":"string","description":"24h HH:MM local time"},"days":{"type":"string","enum":["","daily","weekdays","weekends"],"description":"Empty for once"},"label":{"type":"string"}},"required":["time"]}}},
{"type":"function","function":{"name":"cancel_alarms","description":"Cancel alarms. With time cancels only that alarm, without time cancels all alarms.","parameters":{"type":"object","properties":{"time":{"type":"string","description":"24h HH:MM"}}}}},
{"type":"function","function":{"name":"set_reminder","description":"Set a spoken reminder at a clock time (it says the label aloud).","parameters":{"type":"object","properties":{"time":{"type":"string","description":"24h HH:MM local time"},"label":{"type":"string","description":"What to remember, in Spanish"},"days":{"type":"string","enum":["","daily","weekdays","weekends"]}},"required":["time","label"]}}},
{"type":"function","function":{"name":"set_volume","description":"Set the speaker volume to an absolute level.","parameters":{"type":"object","properties":{"level":{"type":"integer","description":"0-100. If the user says a number 1-10, multiply by 10."}},"required":["level"]}}},
{"type":"function","function":{"name":"change_volume","description":"Raise or lower the volume relative to now (un poco = 10).","parameters":{"type":"object","properties":{"delta":{"type":"integer","description":"Positive to raise, negative to lower"}},"required":["delta"]}}},
{"type":"function","function":{"name":"stop","description":"Stop whatever is sounding: ringing timers or alarms, radio, music, sleep sounds.","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"play_radio","description":"Play music or a configured radio station. Use an empty station when the user just asks for music or radio.","parameters":{"type":"object","properties":{"station":{"type":"string","description":"Station name, or empty"}}}}},
{"type":"function","function":{"name":"media_control","description":"Control current playback.","parameters":{"type":"object","properties":{"action":{"type":"string","enum":["pause","resume","next","previous"]}},"required":["action"]}}},
{"type":"function","function":{"name":"set_do_not_disturb","description":"Turn do-not-disturb mode on or off.","parameters":{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"]}}},
{"type":"function","function":{"name":"daily_text","description":"Read the daily Bible text (texto diario, Examinemos las Escrituras) from wol.jw.org.","parameters":{"type":"object","properties":{"day":{"type":"string","enum":["hoy","mañana","ayer","pasado mañana","anteayer"],"description":"Relative day. Always use this for hoy, mañana, ayer, pasado mañana or anteayer; the device computes the date."},"date":{"type":"string","description":"YYYY-MM-DD, only for a specific calendar date the user names (e.g. el 3 de diciembre). Omit both for today."}}}}},
{"type":"function","function":{"name":"read_watchtower","description":"Read aloud the Watchtower study article (estudio de La Atalaya) from wol.jw.org, paragraph by paragraph with its questions. week: esta (default) or siguiente for next week.","parameters":{"type":"object","properties":{"week":{"type":"string","enum":["esta","siguiente"]}}}}},
{"type":"function","function":{"name":"read_workbook","description":"Read aloud the meeting workbook (Guía de actividades, Vida y Ministerio, guía de estudio de la reunión) of the week.","parameters":{"type":"object","properties":{"week":{"type":"string","enum":["esta","siguiente"]}}}}},
{"type":"function","function":{"name":"read_book_study","description":"Read aloud the congregation Bible study chapter of the week (estudio bíblico de la congregación, estudio de libro).","parameters":{"type":"object","properties":{"week":{"type":"string","enum":["esta","siguiente"]}}}}},
{"type":"function","function":{"name":"jw_help","description":"List everything you can do with jw.org and wol.jw.org (readings, Bible, music) and how to ask for each, when the user asks what you can do with jw.org.","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"read_weekly_bible_reading","description":"Read aloud this week's Bible reading (lectura de la Biblia de la semana) from the workbook.","parameters":{"type":"object","properties":{"week":{"type":"string","enum":["esta","siguiente"]}}}}},
{"type":"function","function":{"name":"read_bible","description":"Read a Bible chapter or verses aloud, e.g. 'lee Juan 3:16' (book Juan, chapter 3, verse_start 16), 'lee el Salmo 23' (whole chapter: omit verses).","parameters":{"type":"object","properties":{"book":{"type":"string","description":"Spanish book name, e.g. Juan, Salmos, Primera de Juan"},"chapter":{"type":"integer"},"verse_start":{"type":"integer"},"verse_end":{"type":"integer"}},"required":["book","chapter"]}}},
{"type":"function","function":{"name":"resume_reading","description":"Continue the last long reading where it stopped ('continúa la lectura', 'sigue leyendo').","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"read_paragraph","description":"Read from a given paragraph of the current reading, or of this week's Watchtower study article ('lee el párrafo 5').","parameters":{"type":"object","properties":{"paragraph":{"type":"integer"}},"required":["paragraph"]}}},
{"type":"function","function":{"name":"play_jw_music","description":"Play a music section from jw.org in random order, e.g. 'reproduce desde jw.org Música internacional'.","parameters":{"type":"object","properties":{"collection":{"type":"string","enum":["Música internacional","Canciones originales","Cantemos con gozo a Jehová (para las reuniones)","Cantemos con gozo a Jehová (cantadas)","Cantemos con gozo a Jehová (instrumental)","Cantemos a Jehová (coro y orquesta)","Canciones para niños","Melodías del Reino"]}},"required":["collection"]}}},
{"type":"function","function":{"name":"play_morning_worship","description":"Play the morning worship programs (adoración matutina) from jw.org: mode reciente for the most recent one, aleatorio (default) for many in random order.","parameters":{"type":"object","properties":{"mode":{"type":"string","enum":["aleatorio","reciente"]}}}}},
{"type":"function","function":{"name":"play_youtube","description":"Play music from YouTube. mode artist: only songs by one artist ('pon canciones de Juan Luis Guerra'); mode song: one specific song, then similar songs ('pon Bachata Rosa', 'pon la canción Provenza de Karol G'); mode like_current: songs similar to the one playing now ('pon algo parecido a esto'). Not for jw.org music or radio stations.","parameters":{"type":"object","properties":{"query":{"type":"string","description":"artist name, or song title with optional artist; empty for like_current"},"mode":{"type":"string","enum":["artist","song","like_current"]}},"required":["mode"]}}},
{"type":"function","function":{"name":"thermostat_status","description":"Say the INDOOR temperature measured by the home thermostat and what the air conditioning or heating is doing. Use it for 'qué temperatura hace en la casa', 'adentro', 'en el apartamento', 'el termostato', '¿está prendido el aire?'. Not for the outdoor weather.","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"set_thermostat","description":"Control the home thermostat: mode off (apaga el aire/la calefacción), heat (calefacción), cool (aire acondicionado), auto; and/or the target temperature in the thermostat's degrees ('pon el aire a 72', 'sube la calefacción a 70').","parameters":{"type":"object","properties":{"mode":{"type":"string","enum":["off","heat","cool","auto"]},"temperature":{"type":"number"}}}}},
{"type":"function","function":{"name":"light_status","description":"Say whether the room light (WiZ bulb) is on, its color and brightness.","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"set_light","description":"Control the room light (WiZ bulb): 'prende la luz' (state on), 'apaga la luz' (state off), 'pon la luz al 50' (brightness 10-100), 'pon la luz azul' (color), 'luz cálida/fría' (white warm/neutral/cool). Combine fields as needed.","parameters":{"type":"object","properties":{"state":{"type":"string","enum":["on","off"]},"brightness":{"type":"number"},"color":{"type":"string","enum":["rojo","naranja","amarillo","verde","azul","morado","rosado","blanco"]},"white":{"type":"string","enum":["warm","neutral","cool"]},"scene":{"type":"string","description":"a built-in scene by its Spanish name: océano, atardecer, chimenea, bosque, jungla, velas, fiesta, club, relajación, concentración, luz nocturna, hora de dormir, despertar, primavera, verano, otoño, navidad, halloween, cielo nevado..."},"effect":{"type":"string","enum":["aurora"],"description":"aurora: northern lights effect ('aurora boreal')"}}}}},
{"type":"function","function":{"name":"light_help","description":"Explain everything the room light (bulb) can do and how to ask for it ('qué podemos hacer con el bombillo').","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"now_playing","description":"Say which song is playing now.","parameters":{"type":"object","properties":{}}}},
{"type":"function","function":{"name":"sleep_sounds","description":"Play relaxing background noise to sleep or concentrate.","parameters":{"type":"object","properties":{"sound":{"type":"string","enum":["white","pink","brown","heartbeat"],"description":"Default brown"},"minutes":{"type":"integer","description":"How long, 0 = until stopped"}}}}}
]`)

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type timerEntry struct {
	ID               int    `json:"id"`
	Kind             string `json:"kind"`
	State            string `json:"state"`
	SecondsRemaining int    `json:"seconds_remaining"`
	Label            string `json:"label"`
	Time             string `json:"time"`
}

// Exec runs one tool call and returns the sentence to speak.
func Exec(api API, name, args string) string {
	if n := resolveTool(name); n != "" {
		name = n
	}
	var a map[string]any
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			if err := json.Unmarshal([]byte(relaxJSON(args)), &a); err != nil {
				logf("bad tool args for %s: %q", name, args)
				return "No entendí bien la orden, ¿me la repites?"
			}
		}
	}
	switch name {
	case "set_timer":
		return setTimer(api, intArg(a, "seconds"), strArg(a, "label"))
	case "cancel_timers":
		return cancelTimers(api, strArg(a, "label"))
	case "list_timers":
		return listTimers(api)
	case "set_alarm":
		return setClock(api, "/alarms", strArg(a, "time"), strArg(a, "days"), strArg(a, "label"), "alarma")
	case "cancel_alarms":
		return cancelAlarms(api, strArg(a, "time"))
	case "set_reminder":
		return setClock(api, "/reminders", strArg(a, "time"), strArg(a, "days"), strArg(a, "label"), "recordatorio")
	case "set_volume":
		return setVolume(api, intArg(a, "level"), false)
	case "change_volume":
		return setVolume(api, intArg(a, "delta"), true)
	case "stop":
		return Stop(api)
	case "play_radio":
		return playRadio(api, strArg(a, "station"))
	case "media_control":
		return mediaControl(api, strArg(a, "action"))
	case "set_do_not_disturb":
		on, _ := a["enabled"].(bool)
		if err := api.Call("PUT", "/dnd", map[string]bool{"enabled": on}, nil); err != nil {
			return failed(err)
		}
		if on {
			return "Listo, activé el modo no molestar."
		}
		return "Listo, desactivé el modo no molestar."
	case "daily_text":
		return dailyText(strArg(a, "day"), strArg(a, "date"))
	case "play_jw_music":
		return playJWMusic(strArg(a, "collection"), true)
	case "read_watchtower":
		return readWatchtower(strArg(a, "week"))
	case "read_workbook":
		return readWorkbook(strArg(a, "week"))
	case "play_morning_worship":
		return playMorningWorship(strArg(a, "mode"))
	case "jw_help":
		return jwHelp
	case "read_book_study":
		return readBookStudy(strArg(a, "week"))
	case "read_weekly_bible_reading":
		return readWeeklyBible(strArg(a, "week"))
	case "read_bible":
		return readBible(strArg(a, "book"), intArg(a, "chapter"), intArg(a, "verse_start"), intArg(a, "verse_end"))
	case "resume_reading":
		return resumeReading()
	case "read_paragraph":
		return readParagraph(intArg(a, "paragraph"))
	case "play_youtube":
		return playYouTube(strArg(a, "query"), strArg(a, "mode"))
	case "thermostat_status":
		return thermostatStatus()
	case "set_thermostat":
		t, ok := a["temperature"].(float64)
		return setThermostat(strArg(a, "mode"), t, ok)
	case "light_status":
		return lightStatus()
	case "set_light":
		b, ok := a["brightness"].(float64)
		return setLight(strArg(a, "state"), strArg(a, "color"), strArg(a, "white"), strArg(a, "scene"), strArg(a, "effect"), b, ok)
	case "light_help":
		return lightHelp
	case "now_playing":
		if svc.Player == nil {
			return "No hay nada sonando de una lista."
		}
		if t, name, ok := svc.Player.Current(); ok {
			if strings.HasPrefix(t.URL, "yt:") {
				return "Está sonando " + withArtist(t) + "."
			}
			return fmt.Sprintf("Está sonando %s, de %s.", spoken(t.Title), name)
		}
		return "No hay nada sonando de una lista."
	case "sleep_sounds":
		return sleepSounds(api, strArg(a, "sound"), intArg(a, "minutes"))
	}
	return "Todavía no sé hacer eso."
}

var toolNames = func() map[string]bool {
	var specs []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(toolSpecs, &specs); err != nil {
		panic(err)
	}
	m := map[string]bool{}
	for _, s := range specs {
		m[s.Function.Name] = true
	}
	return m
}()

func knownTool(name string) bool { return toolNames[name] }

// toolWords translates Spanish words the model sometimes uses in tool
// names (e.g. "cancelar_timers") to the words of the real tool names.
var toolWords = map[string]string{
	"cancelar": "cancel", "borrar": "cancel", "eliminar": "cancel", "quitar": "cancel",
	"listar": "list", "lista": "list", "ver": "list",
	"poner": "set", "fijar": "set", "establecer": "set", "configurar": "set", "crear": "set",
	"cambiar": "change", "subir": "change", "bajar": "change",
	"temporizador": "timer", "temporizadores": "timers", "timer": "timer", "timers": "timers",
	"alarma": "alarm", "alarmas": "alarms", "recordatorio": "reminder", "recordatorios": "reminders",
	"volumen": "volume", "parar": "stop", "detener": "stop",
}

// resolveTool returns the real tool name for name, translating Spanish
// words when needed. It returns "" when nothing matches a real tool.
func resolveTool(name string) string {
	if knownTool(name) {
		return name
	}
	parts := strings.Split(strings.ToLower(name), "_")
	for i, w := range parts {
		if t, ok := toolWords[w]; ok {
			parts[i] = t
		}
	}
	if n := strings.Join(parts, "_"); knownTool(n) {
		return n
	}
	return ""
}

var bareKey = regexp.MustCompile(`([{,]\s*)([A-Za-z_][A-Za-z0-9_]*)\s*:`)

// relaxJSON repairs the pseudo-JSON some models emit in text tool calls,
// e.g. {level:40} or {label:<|"|>basura<|"|>}.
func relaxJSON(s string) string {
	s = strings.ReplaceAll(s, `<|"|>`, `"`)
	s = strings.ReplaceAll(s, "'", `"`)
	return bareKey.ReplaceAllString(s, `$1"$2":`)
}

func failed(err error) string {
	logf("device error: %v", err)
	return "No pude hacerlo, el equipo devolvió un error."
}

func setTimer(api API, secs int, label string) string {
	if secs <= 0 {
		return "¿De cuánto tiempo pongo el temporizador?"
	}
	if secs > 720*60 {
		return "El temporizador puede durar como máximo doce horas."
	}
	label = cleanLabel(label)
	body := map[string]any{"seconds": secs}
	if label != "" {
		body["label"] = label
	}
	if err := api.Call("POST", "/timers", body, nil); err != nil {
		return failed(err)
	}
	if label != "" {
		return fmt.Sprintf("Listo, temporizador de %s para %s.", Duration(secs), label)
	}
	return fmt.Sprintf("Listo, temporizador de %s.", Duration(secs))
}

func timers(api API) ([]timerEntry, error) {
	var t struct {
		Timers []timerEntry `json:"timers"`
	}
	err := api.Call("GET", "/timers", nil, &t)
	return t.Timers, err
}

func cancelTimers(api API, label string) string {
	list, err := timers(api)
	if err != nil {
		return failed(err)
	}
	n := 0
	for _, t := range list {
		if t.Kind == "alarm" {
			continue
		}
		if label != "" && !fuzzyEq(t.Label, label) {
			continue
		}
		if err := api.Call("DELETE", "/timers/"+strconv.Itoa(t.ID), nil, nil); err == nil {
			n++
		}
	}
	switch {
	case n == 0 && label != "":
		return "No encontré un temporizador de " + label + "."
	case n == 0:
		return "No hay temporizadores activos."
	case n == 1:
		return "Listo, cancelé el temporizador."
	}
	return fmt.Sprintf("Listo, cancelé %d temporizadores.", n)
}

func listTimers(api API) string {
	list, err := timers(api)
	if err != nil {
		return failed(err)
	}
	var parts []string
	for _, t := range list {
		name := ""
		if t.Label != "" {
			name = " de " + t.Label
		}
		switch t.Kind {
		case "alarm":
			parts = append(parts, "una alarma a las "+SpokenTime(t.Time)+name)
		case "countdown":
			parts = append(parts, fmt.Sprintf("el temporizador%s, le quedan %s", name, Duration(t.SecondsRemaining)))
		default:
			when := t.Time
			if when != "" {
				when = " a las " + SpokenTime(when)
			}
			parts = append(parts, "un recordatorio"+when+name)
		}
	}
	if len(parts) == 0 {
		return "No tienes temporizadores, alarmas ni recordatorios."
	}
	return "Tienes " + joinES(parts) + "."
}

var hhmm = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

func normTime(s string) (string, bool) {
	m := hhmm.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	h, _ := strconv.Atoi(m[1])
	return fmt.Sprintf("%02d:%s", h, m[2]), true
}

func setClock(api API, path, when, days, label, noun string) string {
	t, ok := normTime(when)
	if !ok {
		return "¿A qué hora pongo la " + noun + "?"
	}
	switch days {
	case "", "daily", "weekdays", "weekends":
	default:
		days = ""
	}
	body := map[string]any{"time": t}
	if l := cleanLabel(label); l != "" {
		body["label"] = l
	}
	if days != "" {
		body["days"] = days
	}
	if err := api.Call("POST", path, body, nil); err != nil {
		return failed(err)
	}
	art := "la"
	if noun == "recordatorio" {
		art = "el"
	}
	return fmt.Sprintf("Listo, puse %s %s a las %s%s.", art, noun, SpokenTime(t), daysES(days))
}

func cancelAlarms(api API, when string) string {
	var al struct {
		Alarms []timerEntry `json:"alarms"`
	}
	if err := api.Call("GET", "/alarms", nil, &al); err != nil {
		return failed(err)
	}
	t, hasTime := normTime(when)
	n := 0
	for _, a := range al.Alarms {
		if hasTime && a.Time != t {
			continue
		}
		if err := api.Call("DELETE", "/alarms/"+strconv.Itoa(a.ID), nil, nil); err == nil {
			n++
		}
	}
	switch {
	case n == 0 && hasTime:
		return "No hay una alarma a las " + SpokenTime(t) + "."
	case n == 0:
		return "No hay alarmas puestas."
	case n == 1:
		return "Listo, cancelé la alarma."
	}
	return fmt.Sprintf("Listo, cancelé %d alarmas.", n)
}

func setVolume(api API, v int, relative bool) string {
	if relative {
		var cur struct {
			Volume int `json:"volume"`
		}
		if err := api.Call("GET", "/audio", nil, &cur); err != nil {
			return failed(err)
		}
		v += cur.Volume
	}
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	if err := api.Call("PUT", "/audio", map[string]int{"volume": v}, nil); err != nil {
		return failed(err)
	}
	return fmt.Sprintf("Volumen en %d.", v)
}

// Stop silences everything; best effort on each subsystem.
func Stop(api API) string {
	if svc.Player != nil {
		svc.Player.Stop()
	}
	_ = api.Call("POST", "/timers/dismiss", map[string]any{}, nil)
	_ = api.Call("POST", "/integrations/radio/stop", map[string]any{}, nil)
	_ = api.Call("DELETE", "/audio/noise", nil, nil)
	_ = api.Call("POST", "/audio/announce/stop", map[string]any{}, nil)
	return "Listo."
}

type station struct {
	Word string `json:"word"`
	Name string `json:"name"`
}

func playRadio(api API, want string) string {
	if svc.Player != nil {
		svc.Player.Stop()
	}
	var r struct {
		Stations []station `json:"stations"`
	}
	if err := api.Call("GET", "/integrations/radio", nil, &r); err != nil {
		return failed(err)
	}
	if len(r.Stations) == 0 {
		return "Todavía no hay emisoras de radio configuradas en el equipo."
	}
	pick := -1
	if strings.TrimSpace(want) == "" {
		pick = 0
	}
	for i, s := range r.Stations {
		if pick >= 0 {
			break
		}
		if fuzzyEq(s.Word, want) || fuzzyEq(s.Name, want) {
			pick = i
			break
		}
	}
	if pick < 0 {
		var names []string
		for _, s := range r.Stations {
			names = append(names, firstNonEmpty(s.Name, s.Word))
		}
		return "No encontré esa emisora. Tengo " + joinES(names) + "."
	}
	s := r.Stations[pick]
	if err := api.Call("POST", "/integrations/radio/play", map[string]string{"word": s.Word}, nil); err != nil {
		return failed(err)
	}
	return "Poniendo " + firstNonEmpty(s.Name, s.Word) + "."
}

func mediaControl(api API, action string) string {
	if p := svc.Player; p != nil && p.Active() {
		switch action {
		case "next", "previous":
			d := 1
			if action == "previous" {
				d = -1
			}
			t, _, err := p.Next(d)
			if err != nil {
				return failed(err)
			}
			if t.Title == "" {
				return "Esa era la última canción de la lista."
			}
			if strings.HasPrefix(t.URL, "yt:") {
				return "Poniendo " + spokenTitle(t) + "."
			}
			return "Poniendo " + t.Title + "."
		case "pause":
			if err := p.Radio.Pause(); err != nil {
				return failed(err)
			}
			return "En pausa."
		case "resume":
			if err := p.Radio.Resume(); err != nil {
				return failed(err)
			}
			return "Sigo."
		}
	}
	m := map[string]string{"pause": "pause", "resume": "play", "next": "next", "previous": "previous"}
	act, ok := m[action]
	if !ok {
		return "No entendí qué hacer con la música."
	}
	if err := api.Call("POST", "/playback/transport", map[string]string{"action": act}, nil); err != nil {
		var ae *apiError
		if asAPIError(err, &ae) && ae.Status < 500 {
			return "No hay nada reproduciéndose que pueda controlar."
		}
		return failed(err)
	}
	return "Listo."
}

func sleepSounds(api API, sound string, minutes int) string {
	names := map[string]string{"white": "ruido blanco", "pink": "ruido rosa", "brown": "ruido marrón", "heartbeat": "latido"}
	if _, ok := names[sound]; !ok {
		sound = "brown"
	}
	if minutes < 0 {
		minutes = 0
	}
	body := map[string]any{"colour": sound, "source": sound, "bed": "none", "level": 30, "minutes": minutes, "fade_seconds": 30}
	if err := api.Call("POST", "/audio/noise", body, nil); err != nil {
		return failed(err)
	}
	if minutes > 0 {
		return fmt.Sprintf("Poniendo %s por %s.", names[sound], Duration(minutes*60))
	}
	return "Poniendo " + names[sound] + "."
}

// ---- Spanish helpers ----

func Duration(secs int) string {
	if secs < 60 {
		return plural(secs, "segundo", "segundos")
	}
	h, m, s := secs/3600, (secs%3600)/60, secs%60
	var parts []string
	if h > 0 {
		parts = append(parts, plural(h, "hora", "horas"))
	}
	if m > 0 {
		parts = append(parts, plural(m, "minuto", "minutos"))
	}
	if s > 0 && h == 0 {
		parts = append(parts, plural(s, "segundo", "segundos"))
	}
	return joinES(parts)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// SpokenTime turns "07:30" into "7:30 de la mañana".
func SpokenTime(t string) string {
	n, ok := normTime(t)
	if !ok {
		return t
	}
	h, _ := strconv.Atoi(n[:2])
	mm := n[3:]
	part := "de la mañana"
	switch {
	case h == 0:
		h, part = 12, "de la noche"
	case h == 12:
		part = "del mediodía"
	case h > 12 && h < 20:
		h, part = h-12, "de la tarde"
	case h >= 20:
		h, part = h-12, "de la noche"
	case h < 6:
		part = "de la madrugada"
	}
	if mm == "00" {
		return fmt.Sprintf("%d %s", h, part)
	}
	return fmt.Sprintf("%d:%s %s", h, mm, part)
}

func daysES(d string) string {
	switch d {
	case "daily":
		return ", todos los días"
	case "weekdays":
		return ", de lunes a viernes"
	case "weekends":
		return ", los fines de semana"
	}
	return ""
}

func joinES(p []string) string {
	switch len(p) {
	case 0:
		return ""
	case 1:
		return p[0]
	}
	return strings.Join(p[:len(p)-1], ", ") + " y " + p[len(p)-1]
}

func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func fuzzyEq(a, b string) bool {
	fa, fb := fold(a), fold(b)
	if fa == "" || fb == "" {
		return false
	}
	return fa == fb || strings.Contains(fa, fb) || strings.Contains(fb, fa)
}

var genericLabels = map[string]bool{"temporizador": true, "timer": true, "alarma": true, "recordatorio": true, "cronometro": true}

func cleanLabel(s string) string {
	s = strings.TrimSpace(s)
	if genericLabels[fold(s)] {
		return ""
	}
	r := []rune(s)
	if len(r) > 32 {
		s = string(r[:32])
	}
	return s
}

func strArg(a map[string]any, k string) string {
	if v, ok := a[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func intArg(a map[string]any, k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Services holds optional integrations; nil fields disable their tools.
type Services struct {
	JW       *JW
	YT       *YouTube
	Taste    *Taste
	Reader   *Reader
	Audio    *audioState // official recording last started, for paragraph jumps
	Player   *Player
	Thermo   *Thermostat
	Light    *Wiz
	Location *time.Location
	Now      func() time.Time
}

var svc = &Services{}

func (s *Services) today() time.Time {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	loc := s.Location
	if loc == nil {
		loc = time.Local
	}
	return now().In(loc)
}

// relativeDays maps spoken relative days to day offsets.
var relativeDays = map[string]int{
	"anteayer": -2, "antier": -2, "ayer": -1, "hoy": 0,
	"mañana": 1, "manana": 1, "pasado mañana": 2, "pasado manana": 2,
}

// dailyText reads the daily text. A relative day wins over a date,
// because the model often miscomputes dates from the context line.
func dailyText(day, date string) string {
	if svc.JW == nil {
		return "Todavía no sé leer el texto diario."
	}
	d := svc.today()
	if off, ok := relativeDays[strings.ToLower(strings.TrimSpace(day))]; ok {
		d = d.AddDate(0, 0, off)
	} else if date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, d.Location())
		if err != nil {
			return "No entendí la fecha del texto."
		}
		d = t
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	text, err := svc.JW.DailyText(ctx, d)
	if err != nil {
		logf("daily text: %v", err)
		return "No pude traer el texto diario de jw.org."
	}
	return text
}

func playJWMusic(collection string, shuffle bool) string {
	if svc.JW == nil || svc.Player == nil {
		return "Todavía no puedo poner música de jw.org."
	}
	key, ok := MatchJWCollection(collection)
	if !ok {
		return "Puedo poner de jw.org: Música internacional, Canciones originales, Cantemos con gozo, Cantemos a Jehová, Canciones para niños y Melodías del Reino. ¿Cuál quieres?"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tracks, name, err := svc.JW.Collection(ctx, key)
	if err != nil {
		logf("jw collection: %v", err)
		return "No pude traer la música de jw.org."
	}
	first, err := startMusic(name, tracks, shuffle)
	if err != nil {
		return failed(err)
	}
	return fmt.Sprintf("Poniendo %s de jw.org en orden aleatorio. Empieza %s.", name, first.Title)
}

// playYouTube starts a YouTube queue: one artist shuffled, one song followed
// by similar songs, or songs similar to the current one.
func playYouTube(query, mode string) string {
	if svc.YT == nil || svc.Player == nil {
		return "Todavía no puedo poner música de YouTube."
	}
	query = strings.TrimSpace(query)
	switch mode {
	case "like_current":
		cur, _, ok := svc.Player.Current()
		if !ok || !strings.HasPrefix(cur.URL, "yt:") {
			return "No está sonando nada de YouTube para buscar parecidas."
		}
		similar, err := svc.YT.Similar(strings.TrimPrefix(cur.URL, "yt:"))
		if err != nil {
			logf("youtube similar: %v", err)
			return "No pude traer canciones parecidas de YouTube."
		}
		queue := svc.Taste.Rank(svc.Taste.Filter(withoutTrack(similar, cur.URL), ""))
		first, err := startMusic("canciones parecidas a "+spokenTitle(cur), queue, false)
		if err != nil {
			return failed(err)
		}
		return fmt.Sprintf("Poniendo canciones parecidas a %s. Empieza %s.", spokenTitle(cur), withArtist(first))
	case "artist":
		if query == "" {
			return "¿Qué canción o artista quieres?"
		}
		tracks, err := svc.YT.Artist(query)
		if err != nil || len(tracks) == 0 {
			logf("youtube artist: %v", err)
			return "No encontré canciones de " + query + " en YouTube."
		}
		name := query
		if main := mainArtist(tracks); main != "" && !similarName(fold(query), fold(main)) {
			name = main // say who was found, not how the recognizer spelled it
		}
		first, err := startMusic("canciones de "+name, svc.Taste.Filter(tracks, name), true)
		if err != nil {
			return failed(err)
		}
		return fmt.Sprintf("Poniendo canciones de %s en orden aleatorio. Empieza %s.", name, spokenTitle(first))
	default: // song
		if query == "" {
			return "¿Qué canción o artista quieres?"
		}
		hits, err := svc.YT.Search(query)
		if err != nil || len(hits) == 0 {
			logf("youtube search: %v", err)
			return "No encontré " + query + " en YouTube."
		}
		song := hits[0]
		queue := []Track{song}
		if similar, err := svc.YT.Similar(strings.TrimPrefix(song.URL, "yt:")); err == nil {
			queue = append(queue, svc.Taste.Rank(svc.Taste.Filter(withoutTrack(similar, song.URL), ""))...)
		} else {
			logf("youtube similar: %v", err)
		}
		if _, err := startMusic(spokenTitle(song)+" y parecidas", queue, false); err != nil {
			return failed(err)
		}
		return fmt.Sprintf("Poniendo %s, y después canciones parecidas.", withArtist(song))
	}
}

func withoutTrack(tracks []Track, url string) []Track {
	out := make([]Track, 0, len(tracks))
	for _, t := range tracks {
		if t.URL != url {
			out = append(out, t)
		}
	}
	return out
}

func withArtist(t Track) string {
	if t.Artist == "" {
		return spokenTitle(t)
	}
	return spokenTitle(t) + ", de " + t.Artist
}

// spokenTitle cleans a YouTube title for speech: drops bracketed notes like
// "(Video Oficial)" and an "Artist - " prefix.
func spokenTitle(t Track) string {
	title := t.Title
	var b strings.Builder
	depth := 0
	for _, r := range title {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	clean := strings.Join(strings.Fields(b.String()), " ")
	if i := strings.Index(clean, " - "); i >= 0 {
		if t.Artist == "" || strings.Contains(fold(clean[:i]), fold(t.Artist)) {
			clean = strings.TrimSpace(clean[i+3:])
		}
	}
	if clean == "" {
		return title
	}
	return clean
}
