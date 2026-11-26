// Command puente is an OpenAI-compatible proxy for LibreEcho agentd.
// It adds Spanish tool calling (timers, alarms, volume, radio...) on top of
// an upstream chat model and executes actions through the device web API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	_ "time/tzdata"

	"golang.org/x/text/unicode/norm"
)

const systemPrompt = `Eres Mia, la asistente de voz de un parlante en casa; hablas en femenino (por ejemplo "estoy lista"). Respondes siempre en español latino neutro, natural y breve (una o dos frases cortas), sin markdown, listas, enlaces ni emojis, porque tu respuesta se lee en voz alta.
Cuando el usuario pida una acción del equipo (temporizador, alarma, recordatorio, volumen, parar, radio, música, no molestar, sonidos para dormir), llama a la herramienta correspondiente; no describas la acción en texto. Para recordatorios relativos ("en 20 minutos") usa set_timer con la etiqueta. Si piden canciones o música de jw.org o de una de sus secciones (Música internacional, Canciones originales, Cantemos con gozo, Cantemos a Jehová, Canciones para niños, Melodías del Reino) usa play_jw_music. Si piden música o radio sin decir cuál, llama a play_radio con station vacío; no preguntes qué tipo de música. Si piden el texto diario o el texto del día, usa daily_text y no lo resumas.
Para preguntas de conocimiento o conversación responde directamente, sin herramientas. Usa la fecha y hora del contexto para preguntas de hora o fecha. Si no tienes un dato (por ejemplo el clima), dilo con honestidad. Nunca digas que hiciste algo que no hiciste.`

var (
	verbose bool
	ctxLine = regexp.MustCompile(`(?s)The current local date and time is .*`)
)

func logf(format string, a ...any) { log.Printf(format, a...) }

type chatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Extra      json.RawMessage `json:"-"`
}

type inbound struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type server struct {
	upstream string
	model    string // optional override
	api      API
	client   *http.Client
	tail     *tailSpeaker // speaks what does not fit agentd's reply buffer
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18080", "listen address")
	upstream := flag.String("upstream", "https://integrate.api.nvidia.com/v1", "upstream OpenAI-compatible base URL")
	device := flag.String("device", "http://127.0.0.1:8080/api/v1", "LibreEcho web API base")
	creds := flag.String("creds", "/data/libreecho/local/puente.creds", "file with device username and password")
	tasteFile := flag.String("taste", "/data/libreecho/local/puente-taste.json", "file where the bridge learns which YouTube songs and artists the owner plays or skips")
	model := flag.String("model", "", "override model (default: the one agentd sends)")
	flag.BoolVar(&verbose, "v", false, "log transcripts")
	wyAddr := flag.String("wyoming", "", "serve Wyoming TTS on this address (e.g. 127.0.0.1:10200); empty disables")
	ttsVoice := flag.String("tts-voice", "Magpie-Multilingual.ES-US.Mia", "NVIDIA Magpie voice")
	keyFile := flag.String("key-file", "/data/libreecho/secrets/openai-codex.json", "JSON file holding the NVIDIA api_key")
	localTTS := flag.String("local-tts", "/run/libreecho/tts-local.sock", "fallback local ttsd socket")
	agentSock := flag.String("agent-sock", "/run/libreecho/agent.sock", "agentd socket, polled to lower the music while it listens")
	mixerStatus := flag.String("mixer-status", "/run/libreecho-audio/status.json", "audiod state file, read to know when the assistant has finished speaking")
	mainTTS := flag.String("main-tts", "/run/libreecho/tts.sock", "main ttsd socket, used to speak the part of a long reply that agentd drops")
	tz := flag.String("tz", "America/Chicago", "time zone for dates (daily text)")
	radioSock := flag.String("radio", "/run/libreecho/radio.sock", "radiod socket for music queues")
	ttsTest := flag.String("tts-test", "", "synthesize this text with the cloud voice, write raw PCM to stdout and exit")
	flag.Parse()
	if *ttsTest != "" {
		r := &Riva{URL: "https://grpc.nvcf.nvidia.com/nvidia.riva.tts.RivaSpeechSynthesis/Synthesize",
			FunctionID: "877104f7-e885-42b9-8de8-f6e4c6303969", KeyFile: *keyFile, Voice: *ttsVoice,
			Language: "es-US", Rate: 22050, HTTP: &http.Client{Timeout: 10 * time.Second}}
		t0 := time.Now()
		pcm, err := r.Synthesize(context.Background(), cleanForCloud(*ttsTest))
		if err != nil {
			log.SetOutput(os.Stderr)
			log.Fatalf("tts-test: %v", err)
		}
		fmt.Fprintf(os.Stderr, "tts-test: %d bytes (%.2fs audio) in %dms\n", len(pcm), float64(len(pcm))/2/22050, time.Since(t0).Milliseconds())
		os.Stdout.Write(pcm)
		return
	}

	if loc, err := time.LoadLocation(*tz); err == nil {
		svc.Location = loc
	} else {
		log.Printf("time zone %s: %v (using UTC)", *tz, err)
	}
	svc.JW = &JW{HTTP: &http.Client{Timeout: 10 * time.Second}, Location: svc.Location,
		WOLBase: "https://wol.jw.org", Mediator: "https://b.jw-cdn.org/apis/mediator/v1",
		PubMedia: "https://b.jw-cdn.org/apis/pub-media/GETPUBMEDIALINKS"}
	svc.Taste = LoadTaste(*tasteFile)
	svc.YT = &YouTube{HTTP: &http.Client{Timeout: 15 * time.Second},
		Web: "https://www.youtube.com", Music: "https://music.youtube.com"}
	radiod := &RadioSock{Path: *radioSock}
	bus := &BusPlayer{
		Bus:     "/run/libreecho-audio/media.pcm",
		HTTP:    &http.Client{Timeout: 0},
		Release: func() { _ = radiod.Stop() },
		YT:      svc.YT,
	}
	svc.Player = &Player{AlwaysAdvance: true, Radio: bus, OnEnd: svc.Taste.Record}

	user, pass, err := LoadCreds(*creds)
	if err != nil {
		log.Fatalf("credentials: %v", err)
	}
	voice := &announceVoice{TTS: &LocalTTS{Socket: *mainTTS}, Status: *mixerStatus}
	s := &server{
		upstream: strings.TrimRight(*upstream, "/"),
		model:    *model,
		api:      &Device{Base: strings.TrimRight(*device, "/"), User: user, Password: pass, HTTP: &http.Client{Timeout: 8 * time.Second}},
		client:   &http.Client{Timeout: 25 * time.Second},
		tail:     &tailSpeaker{Voice: voice, Poll: 200 * time.Millisecond, Idle: 2500 * time.Millisecond, StartWait: 15 * time.Second},
	}
	svc.Reader = &Reader{Voice: voice, Poll: 200 * time.Millisecond, Idle: 2500 * time.Millisecond,
		StartWait: 15 * time.Second, Pause: 700 * time.Millisecond}
	// Watch agentd's voice turns: hold the bridge's playback while it
	// listens or answers, and cut the bridge's own speech (long reply tail,
	// reading) the moment it starts listening.
	hold := &voiceHold{}
	go (&duckWatch{Poll: 100 * time.Millisecond, Hold: 600 * time.Millisecond,
		Duck:  func(on bool) { hold.Set(on); logf("voice hold %v", on) },
		State: func() (bool, bool, error) { return agentState(*agentSock) },
		OnListen: func() {
			if s.tail.Speaking() || svc.Reader.Active() {
				s.tail.Cancel()
				svc.Reader.Stop() // keeps the place: "sigue" goes on
				_ = s.api.Call("POST", "/audio/announce/stop", map[string]any{}, nil)
				logf("listening: stopped the bridge's speech")
			}
		}}).Run(context.Background())
	if *wyAddr != "" {
		l, err := net.Listen("tcp", *wyAddr)
		if err != nil {
			log.Fatalf("wyoming listen: %v", err)
		}
		wy := &WyomingServer{
			Synth: &Riva{
				URL:        "https://grpc.nvcf.nvidia.com/nvidia.riva.tts.RivaSpeechSynthesis/Synthesize",
				FunctionID: "877104f7-e885-42b9-8de8-f6e4c6303969",
				KeyFile:    *keyFile, Voice: *ttsVoice, Language: "es-US", Rate: 22050,
				HTTP: &http.Client{Timeout: 10 * time.Second},
			},
			Fallback: &LocalTTS{Socket: *localTTS},
			Voice:    *ttsVoice, Rate: 22050, Timeout: 6 * time.Second,
		}
		log.Printf("wyoming tts on %s voice %s", *wyAddr, *ttsVoice)
		go func() { log.Fatal(wy.Serve(l)) }()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.chat)
	mux.HandleFunc("/chat/completions", s.chat)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	log.Printf("puente listening on %s -> %s", *listen, s.upstream)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in inbound
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.tail.Cancel()                          // a new turn interrupts the rest of a long reply
	token, reading := svc.Reader.Interrupt() // and a reading in progress
	turnReading = reading
	defer func() { turnReading = false }()
	start := time.Now()
	reply := s.answer(r.Context(), in, r.Header.Get("Authorization"))
	if verbose {
		logf("turn %q -> %q (%dms)", lastUser(in.Messages), reply, time.Since(start).Milliseconds())
	} else {
		logf("turn done in %dms", time.Since(start).Milliseconds())
	}
	head, rest := splitForAgentd(reply, agentdReplyMax)
	if s.tail == nil {
		head, rest = reply, nil
	}
	writeSSE(w, head)
	s.tail.Start(rest)
	// After a quick question or a false wake word, go back to the reading,
	// as speakers do with music. Not after a stop word, or when the turn
	// started music or touched the reading itself.
	if reading && !isControl(stripWake(lastUser(in.Messages))) && (svc.Player == nil || !svc.Player.Active()) {
		svc.Reader.ResumeIfUntouched(token)
	}
}

func lastUser(ms []chatMessage) string {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Role == "user" {
			return ms[i].Content
		}
	}
	return ""
}

var stopWords = map[string]bool{
	"para": true, "paralo": true, "parala": true, "detente": true, "basta": true, "silencio": true,
	"callate": true, "ya": true, "ya para": true, "deten": true, "apagalo": true, "apaga la alarma": true,
	"para la alarma": true, "para la musica": true, "para la radio": true, "stop": true, "alto": true,
}

// answer returns the sentence to speak for one turn.
func (s *server) answer(ctx context.Context, in inbound, auth string) string {
	user := stripWake(strings.TrimSpace(lastUser(in.Messages)))
	if fold(user) == "" { // nothing heard (the recognizer writes ".")
		return "No te escuché bien."
	}
	setLastUser(in.Messages, user)
	if stopWords[fold(user)] {
		svc.Reader.Forget() // "para" ends the reading for good
		pausedPlayer = false
		return Stop(s.api)
	}
	if pauseWords[fold(user)] {
		return pauseAll()
	}
	if resumeWords[fold(user)] {
		return resumeAll()
	}
	if isJWHelpQuestion(user) {
		return jwHelp
	}
	sys := systemPrompt
	for _, m := range in.Messages {
		if m.Role == "system" {
			if c := ctxLine.FindString(m.Content); c != "" {
				sys += "\nContexto: " + c
			}
		}
	}
	model := in.Model
	if s.model != "" {
		model = s.model
	}
	msgs := []chatMessage{{Role: "system", Content: sys}, {Role: "user", Content: user}}
	resp, err := s.complete(ctx, model, msgs, auth)
	if err != nil {
		logf("upstream error: %v", err)
		return "No pude conectarme con el servicio, intenta de nuevo en un momento."
	}
	if len(resp.ToolCalls) > 0 {
		var said []string
		for i, tc := range resp.ToolCalls {
			if i >= 4 {
				break
			}
			if verbose {
				logf("tool %s %s", tc.Function.Name, tc.Function.Arguments)
			}
			said = append(said, Exec(s.api, tc.Function.Name, tc.Function.Arguments))
		}
		return dedupe(said)
	}
	if name, args, ok := textToolCall(resp.Content); ok {
		if verbose {
			logf("text tool %s %s", name, args)
		}
		return Exec(s.api, name, args)
	}
	text := cleanSpeech(resp.Content)
	if text == "" {
		return "No tengo una respuesta para eso."
	}
	return text
}

var textCall = regexp.MustCompile(`(?s)^\s*(?:<tool_call>)?\s*([a-z_]+)\s*\(?\s*(\{.*?\})?\s*\)?\s*(?:</tool_call>)?\s*$`)

// textToolCall recognizes tool calls the model wrote as plain text,
// e.g. "list_timers{}", "set_volume({\"level\":30})" or
// {"name":"stop","arguments":{}}.
func textToolCall(content string) (string, string, bool) {
	c := strings.TrimSpace(content)
	if strings.HasPrefix(c, "{") {
		var j struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal([]byte(c), &j) == nil && resolveTool(j.Name) != "" {
			j.Name = resolveTool(j.Name)
			args := string(j.Arguments)
			var str string
			if json.Unmarshal(j.Arguments, &str) == nil {
				args = str
			}
			return j.Name, args, true
		}
		return "", "", false
	}
	m := textCall.FindStringSubmatch(c)
	if m == nil {
		return "", "", false
	}
	name := resolveTool(m[1])
	if name == "" {
		return "", "", false
	}
	return name, m[2], true
}

func dedupe(p []string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range p {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

var mdJunk = strings.NewReplacer("**", "", "__", "", "`", "", "#", "", "* ", "", "—", ", ")

func cleanSpeech(s string) string {
	s = mdJunk.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

type upstreamResp struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (s *server) complete(ctx context.Context, model string, msgs []chatMessage, auth string) (chatMessage, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    msgs,
		"tools":       toolSpecs,
		"tool_choice": "auto",
		"temperature": 0.2,
		"max_tokens":  400,
		"stream":      false,
	})
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", s.upstream+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return chatMessage{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			last = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
			time.Sleep(600 * time.Millisecond)
			continue
		}
		if resp.StatusCode != 200 {
			return chatMessage{}, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, b)
		}
		var u upstreamResp
		if err := json.Unmarshal(b, &u); err != nil {
			return chatMessage{}, err
		}
		if len(u.Choices) == 0 {
			return chatMessage{}, errors.New("no choices")
		}
		return u.Choices[0].Message, nil
	}
	return chatMessage{}, last
}

// writeSSE emits the reply as an OpenAI-style chat.completion.chunk stream.
func writeSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	id := fmt.Sprintf("puente-%d", time.Now().UnixNano())
	chunk := func(delta map[string]string, finish any) {
		b, _ := json.Marshal(map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(),
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	// agentd reads the stream line by line with a bounded buffer: a long reply
	// in one event is dropped silently, so send it in small pieces.
	for i, piece := range sseChunks(Speakable(text), 120) {
		delta := map[string]string{"content": piece}
		if i == 0 {
			delta["role"] = "assistant"
		}
		chunk(delta, nil)
	}
	chunk(map[string]string{}, "stop")
	io.WriteString(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// respell writes foreign words the way the Spanish voice should say them.
var respell = strings.NewReplacer(
	"°F", " grados fárenjait", "°C", " grados centígrados",
	"Fahrenheit", "fárenjait", "fahrenheit", "fárenjait",
)

// Speakable works around the device TTS (sherpa-onnx 1.12.15 + espeak-ng),
// which spells precomposed accented vowels ("qué" -> "qu e acentuada").
// Decomposing only those vowels (NFD) makes espeak read them correctly;
// ñ stays precomposed because it already works.
func Speakable(s string) string {
	s = respell.Replace(s)
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("áéíóúÁÉÍÓÚüÜ", r) {
			b.WriteString(norm.NFD.String(string(r)))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sseChunks splits text into pieces of at most max bytes, cutting after a
// space when possible and never inside a UTF-8 sequence.
func sseChunks(text string, max int) []string {
	var out []string
	for len(text) > max {
		cut := strings.LastIndex(text[:max], " ")
		if cut <= 0 {
			cut = max
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
		} else {
			cut++ // keep the space with the left piece
		}
		out = append(out, text[:cut])
		text = text[cut:]
	}
	if text != "" || len(out) == 0 {
		out = append(out, text)
	}
	return out
}

func asAPIError(err error, target **apiError) bool { return errors.As(err, target) }

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
}
