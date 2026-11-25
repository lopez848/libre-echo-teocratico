package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type call struct {
	Method, Path string
	Body         string
}

type fakeAPI struct {
	calls     []call
	responses map[string]string // "METHOD path" -> JSON data
}

func (f *fakeAPI) Call(method, path string, payload any, out any) error {
	b := ""
	if payload != nil {
		j, _ := json.Marshal(payload)
		b = string(j)
	}
	f.calls = append(f.calls, call{method, path, b})
	if r, ok := f.responses[method+" "+path]; ok && out != nil {
		return json.Unmarshal([]byte(r), out)
	}
	return nil
}

func (f *fakeAPI) find(method, path string) *call {
	for i := range f.calls {
		if f.calls[i].Method == method && f.calls[i].Path == path {
			return &f.calls[i]
		}
	}
	return nil
}

func TestSpanishFormatting(t *testing.T) {
	cases := map[int]string{30: "30 segundos", 60: "1 minuto", 300: "5 minutos", 5400: "1 hora y 30 minutos", 90: "1 minuto y 30 segundos"}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%d)=%q want %q", in, got, want)
		}
	}
	times := map[string]string{"07:30": "7:30 de la mañana", "7:00": "7 de la mañana", "13:15": "1:15 de la tarde", "21:00": "9 de la noche", "00:10": "12:10 de la noche", "12:00": "12 del mediodía"}
	for in, want := range times {
		if got := SpokenTime(in); got != want {
			t.Errorf("SpokenTime(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSpeakableDecomposesAccentsButKeepsEnye(t *testing.T) {
	got := Speakable("¿En qué? mañana")
	if strings.ContainsRune(got, 'é') || !strings.Contains(got, "é") {
		t.Fatalf("accent not decomposed: %q", got)
	}
	if !strings.ContainsRune(got, 'ñ') {
		t.Fatalf("ñ must stay precomposed: %q", got)
	}
}

func TestSetTimer(t *testing.T) {
	f := &fakeAPI{}
	got := Exec(f, "set_timer", `{"seconds":300,"label":"arroz"}`)
	c := f.find("POST", "/timers")
	if c == nil || c.Body != `{"label":"arroz","seconds":300}` {
		t.Fatalf("calls=%+v", f.calls)
	}
	if got != "Listo, temporizador de 5 minutos para arroz." {
		t.Fatalf("reply=%q", got)
	}
	if Exec(f, "set_timer", `{"seconds":0}`) != "¿De cuánto tiempo pongo el temporizador?" {
		t.Fatal("zero seconds must ask")
	}
}

func TestCancelTimersByLabelSkipsAlarms(t *testing.T) {
	f := &fakeAPI{responses: map[string]string{"GET /timers": `{"timers":[{"id":1,"kind":"countdown","label":"arroz"},{"id":2,"kind":"countdown","label":"pasta"},{"id":3,"kind":"alarm","label":"arroz"}]}`}}
	got := Exec(f, "cancel_timers", `{"label":"el arroz"}`)
	if f.find("DELETE", "/timers/1") == nil || f.find("DELETE", "/timers/2") != nil || f.find("DELETE", "/timers/3") != nil {
		t.Fatalf("calls=%+v", f.calls)
	}
	if got != "Listo, cancelé el temporizador." {
		t.Fatalf("reply=%q", got)
	}
}

func TestAlarmAndReminder(t *testing.T) {
	f := &fakeAPI{}
	if got := Exec(f, "set_alarm", `{"time":"6:45","days":"weekdays"}`); got != "Listo, puse la alarma a las 6:45 de la mañana, de lunes a viernes." {
		t.Fatalf("reply=%q", got)
	}
	if c := f.find("POST", "/alarms"); c == nil || c.Body != `{"days":"weekdays","time":"06:45"}` {
		t.Fatalf("calls=%+v", f.calls)
	}
	if got := Exec(f, "set_reminder", `{"time":"25:00","label":"x"}`); !strings.HasPrefix(got, "¿A qué hora") {
		t.Fatalf("invalid time must ask, got %q", got)
	}
}

func TestRelativeVolumeClamps(t *testing.T) {
	f := &fakeAPI{responses: map[string]string{"GET /audio": `{"volume":95}`}}
	if got := Exec(f, "change_volume", `{"delta":10}`); got != "Volumen en 100." {
		t.Fatalf("reply=%q", got)
	}
	if c := f.find("PUT", "/audio"); c == nil || c.Body != `{"volume":100}` {
		t.Fatalf("calls=%+v", f.calls)
	}
}

func TestRadioWithoutStations(t *testing.T) {
	f := &fakeAPI{responses: map[string]string{"GET /integrations/radio": `{"stations":[]}`}}
	if got := Exec(f, "play_radio", `{"station":"noticias"}`); !strings.Contains(got, "no hay emisoras") {
		t.Fatalf("reply=%q", got)
	}
}

func newUpstream(t *testing.T, reply string, hits *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth not forwarded: %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, reply)
	}))
}

func TestAnswerExecutesToolCall(t *testing.T) {
	hits := 0
	up := newUpstream(t, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"set_volume","arguments":"{\"level\":30}"}}]}}]}`, &hits)
	defer up.Close()
	f := &fakeAPI{}
	s := &server{upstream: up.URL, api: f, client: up.Client()}
	in := inbound{Model: "m", Messages: []chatMessage{{Role: "system", Content: "x The current local date and time is Monday."}, {Role: "user", Content: "bájale a 30"}}}
	if got := s.answer(context.Background(), in, "Bearer k"); got != "Volumen en 30." {
		t.Fatalf("reply=%q", got)
	}
	if hits != 1 || f.find("PUT", "/audio") == nil {
		t.Fatalf("hits=%d calls=%+v", hits, f.calls)
	}
}

func TestStopWordSkipsUpstream(t *testing.T) {
	hits := 0
	up := newUpstream(t, `{}`, &hits)
	defer up.Close()
	f := &fakeAPI{}
	s := &server{upstream: up.URL, api: f, client: up.Client()}
	got := s.answer(context.Background(), inbound{Messages: []chatMessage{{Role: "user", Content: "¡Para!"}}}, "Bearer k")
	if got != "Listo." || hits != 0 || f.find("POST", "/timers/dismiss") == nil {
		t.Fatalf("got=%q hits=%d calls=%+v", got, hits, f.calls)
	}
}

func TestUpstreamFailureStillSpeaks(t *testing.T) {
	s := &server{upstream: "http://127.0.0.1:1", api: &fakeAPI{}, client: http.DefaultClient}
	got := s.answer(context.Background(), inbound{Messages: []chatMessage{{Role: "user", Content: "hola"}}}, "")
	if !strings.HasPrefix(got, "No pude conectarme") {
		t.Fatalf("got=%q", got)
	}
}

func TestSSEFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSSE(rec, "Hola")
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.HasSuffix(body, "data: [DONE]\n\n") || !strings.Contains(body, `"content":"Hola"`) {
		t.Fatalf("body=%q", body)
	}
}

func TestTextToolCallForms(t *testing.T) {
	cases := map[string]string{
		"list_timers{}":                  "list_timers|{}",
		"list_timers":                    "list_timers|",
		`set_volume({"level":30})`:       `set_volume|{"level":30}`,
		`{"name":"stop","arguments":{}}`: "stop|{}",
	}
	for in, want := range cases {
		n, a, ok := textToolCall(in)
		if !ok || n+"|"+a != want {
			t.Errorf("textToolCall(%q)=%q,%q,%v want %q", in, n, a, ok, want)
		}
	}
	for _, in := range []string{"La capital es Santiago.", "hola", "unknown_tool{}"} {
		if _, _, ok := textToolCall(in); ok {
			t.Errorf("%q must not be a tool call", in)
		}
	}
}

func TestGenericTimerLabelDropped(t *testing.T) {
	f := &fakeAPI{}
	if got := Exec(f, "set_timer", `{"seconds":300,"label":"Temporizador"}`); got != "Listo, temporizador de 5 minutos." {
		t.Fatalf("reply=%q", got)
	}
}

func TestRelaxedTextArgs(t *testing.T) {
	f := &fakeAPI{}
	if got := Exec(f, "set_volume", `{level:40}`); got != "Volumen en 40." {
		t.Fatalf("reply=%q", got)
	}
	got := Exec(f, "set_reminder", `{label:<|"|>sacar la basura<|"|>,time:<|"|>20:00<|"|>}`)
	if got != "Listo, puse el recordatorio a las 8 de la noche." {
		t.Fatalf("reply=%q", got)
	}
	if c := f.find("POST", "/reminders"); c == nil || c.Body != `{"label":"sacar la basura","time":"20:00"}` {
		t.Fatalf("calls=%+v", f.calls)
	}
}

func TestSpeakableRespellsFahrenheit(t *testing.T) {
	got := Speakable("Hace 25 grados Fahrenheit, 30°F")
	if strings.Contains(got, "Fahrenheit") || strings.Contains(got, "°") || !strings.Contains(got, "fárenjait") {
		t.Fatalf("got %q", got)
	}
}

func TestPlayRadioWithoutNamePlaysFirstStation(t *testing.T) {
	f := &fakeAPI{responses: map[string]string{"GET /integrations/radio": `{"stations":[{"word":"canticos","name":"Cánticos"},{"word":"otra","name":"Otra"}]}`}}
	if got := Exec(f, "play_radio", `{"station":""}`); got != "Poniendo Cánticos." {
		t.Fatalf("reply=%q", got)
	}
	if c := f.find("POST", "/integrations/radio/play"); c == nil || c.Body != `{"word":"canticos"}` {
		t.Fatalf("calls=%+v", f.calls)
	}
	f2 := &fakeAPI{responses: map[string]string{"GET /integrations/radio": `{"stations":[{"word":"canticos","name":"Cánticos"},{"word":"otra","name":"Otra"}]}`}}
	if got := Exec(f2, "play_radio", `{"station":"otra"}`); got != "Poniendo Otra." {
		t.Fatalf("named station reply=%q", got)
	}
}

func TestSSESplitsLongRepliesIntoSmallEvents(t *testing.T) {
	long := strings.Repeat("Que la gente sepa que tu nombre es Jehová. ", 40)
	rec := httptest.NewRecorder()
	writeSSE(rec, long)
	var got strings.Builder
	events := 0
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		if len(line) > 400 {
			t.Fatalf("event too long: %d bytes", len(line))
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		got.WriteString(ev.Choices[0].Delta.Content)
		events++
	}
	if events < 10 || got.String() != Speakable(long) {
		t.Fatalf("events=%d reassembled ok=%v", events, got.String() == Speakable(long))
	}
}
