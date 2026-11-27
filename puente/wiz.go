package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"
)

// Wiz controls a WiZ (Philips) bulb through its local UDP API on port
// 38899. The bulb is found by broadcasting getPilot, so a new address from
// the router does not matter.
type Wiz struct {
	Broadcast string // where discovery goes; default 255.255.255.255:38899

	EffectStep time.Duration // time between effect updates; default 250 ms

	mu   sync.Mutex
	addr string // last address the bulb answered from

	emu        sync.Mutex
	stopEffect context.CancelFunc
	effectDone chan struct{}
}

// effectRunning reports whether the aurora is on.
func (w *Wiz) effectRunning() bool {
	w.emu.Lock()
	defer w.emu.Unlock()
	return w.stopEffect != nil
}

// StopEffect ends the running light effect, if any.
func (w *Wiz) StopEffect() {
	if w == nil {
		return
	}
	w.emu.Lock()
	defer w.emu.Unlock()
	if w.stopEffect != nil {
		w.stopEffect()
		<-w.effectDone // no update may arrive after the effect is stopped
		w.stopEffect, w.effectDone = nil, nil
	}
}

// auroraColors are the stops of the aurora effect: greens, teals, blues and
// violets, like a northern lights sky.
var auroraColors = [][3]float64{
	{0, 255, 90}, {0, 220, 150}, {0, 170, 255}, {60, 255, 120}, {110, 0, 255}, {0, 255, 60}, {170, 0, 220}, {0, 200, 200},
}

// startAurora moves the bulb smoothly between aurora colors until stopped.
func (w *Wiz) startAurora() {
	w.StopEffect()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w.emu.Lock()
	w.stopEffect, w.effectDone = cancel, done
	w.emu.Unlock()
	step := w.EffectStep
	if step == 0 {
		step = 250 * time.Millisecond
	}
	go func() {
		defer close(done)
		cur := auroraColors[0]
		for i := 1; ; i++ {
			next := auroraColors[rand.IntN(len(auroraColors))]
			steps := 16 + rand.IntN(16) // 4 to 8 s per move at 250 ms
			dim := 55 + float64(rand.IntN(46))
			for k := 1; k <= steps; k++ {
				f := float64(k) / float64(steps)
				f = f * f * (3 - 2*f) // ease in and out
				var c [3]float64
				for j := range c {
					c[j] = math.Round(cur[j] + (next[j]-cur[j])*f)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(step):
				}
				if ctx.Err() != nil {
					return
				}
				w.call("setPilot", map[string]any{"state": true, "r": c[0], "g": c[1], "b": c[2], "dimming": dim})
			}
			cur = next
		}
	}()
}

// lightScenes are the bulb's built-in scenes (WiZ sceneId), by spoken name.
var lightScenes = map[string]int{
	"oceano": 1, "romance": 2, "romantica": 2, "atardecer": 3, "puesta de sol": 3, "fiesta": 4, "chimenea": 5, "acogedor": 6,
	"acogedora": 6, "bosque": 7, "colores pastel": 8, "pastel": 8, "despertar": 9, "amanecer": 9, "hora de dormir": 10,
	"dormir": 10, "luz de dia": 12, "luz nocturna": 14, "noche": 14, "concentracion": 15, "relajacion": 16, "relajar": 16,
	"colores reales": 17, "television": 18, "tele": 18, "plantas": 19, "primavera": 20, "verano": 21, "otono": 22,
	"inmersion": 23, "fondo del mar": 23, "jungla": 24, "selva": 24, "mojito": 25, "club": 26, "discoteca": 26,
	"navidad": 27, "halloween": 28, "velas": 29, "luz de velas": 29, "blanco dorado": 30, "pulso": 31, "steampunk": 32,
	"diwali": 33, "cielo nevado": 36, "nieve": 36,
}

// sceneNames is how each scene is said back.
var sceneNames = map[int]string{
	1: "océano", 2: "romance", 3: "atardecer", 4: "fiesta", 5: "chimenea", 6: "acogedor", 7: "bosque", 8: "colores pastel",
	9: "despertar", 10: "hora de dormir", 12: "luz de día", 14: "luz nocturna", 15: "concentración", 16: "relajación",
	17: "colores reales", 18: "televisión", 19: "plantas", 20: "primavera", 21: "verano", 22: "otoño", 23: "inmersión",
	24: "jungla", 25: "mojito", 26: "club", 27: "navidad", 28: "halloween", 29: "velas", 30: "blanco dorado", 31: "pulso",
	32: "steampunk", 33: "diwali", 36: "cielo nevado",
}

// whiteScenes are scenes that are just a white tone: said as that white.
var whiteScenes = map[int]bool{11: true, 12: true, 13: true, 30: true, 34: true}

var errNoBulb = errors.New("bulb not found")

type pilot struct {
	State   bool     `json:"state"`
	Dimming float64  `json:"dimming"`
	Temp    float64  `json:"temp"`
	SceneID float64  `json:"sceneId"`
	R       *float64 `json:"r"`
	G       *float64 `json:"g"`
	B       *float64 `json:"b"`
}

// call sends one request and returns the raw result object.
func (w *Wiz) call(method string, params map[string]any) (json.RawMessage, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	req, _ := json.Marshal(map[string]any{"method": method, "params": params})
	for attempt := 0; attempt < 2; attempt++ {
		if w.addr == "" {
			if err := w.discover(); err != nil {
				return nil, err
			}
		}
		res, err := exchange(w.addr, req, time.Second)
		if err == nil {
			return res, nil
		}
		w.addr = "" // maybe it moved: look for it again
	}
	return nil, errNoBulb
}

func (w *Wiz) discover() error {
	target := w.Broadcast
	if target == "" {
		target = "255.255.255.255:38899"
	}
	dst, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return err
	}
	pc, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer pc.Close()
	buf := make([]byte, 2048)
	for i := 0; i < 3; i++ {
		pc.WriteTo([]byte(`{"method":"getPilot","params":{}}`), dst)
		pc.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
		if _, from, err := pc.ReadFrom(buf); err == nil {
			w.addr = from.String()
			return nil
		}
	}
	return errNoBulb
}

func exchange(addr string, req []byte, timeout time.Duration) (json.RawMessage, error) {
	c, err := net.Dial("udp4", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write(req); err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("wiz: %s", resp.Error.Message)
	}
	return resp.Result, nil
}

var lightColors = map[string][3]float64{
	"rojo": {255, 0, 0}, "naranja": {255, 120, 0}, "amarillo": {255, 200, 0}, "verde": {0, 255, 0},
	"azul": {0, 0, 255}, "morado": {140, 0, 255}, "rosado": {255, 60, 150},
}

var whiteTemps = map[string]float64{"warm": 2700, "neutral": 4200, "cool": 6500}

func whiteName(temp float64) string {
	switch {
	case temp <= 3200:
		return "blanco cálido"
	case temp <= 5000:
		return "blanco neutro"
	}
	return "blanco frío"
}

func lightError(err error) string {
	if errors.Is(err, errNoBulb) {
		return "No encontré el bombillo en la red."
	}
	logf("light: %v", err)
	return "No pude comunicarme con el bombillo."
}

func lightStatus() string {
	if svc.Light == nil {
		return "No hay ningún bombillo configurado."
	}
	if svc.Light.effectRunning() {
		return "La luz está con la aurora boreal."
	}
	raw, err := svc.Light.call("getPilot", map[string]any{})
	if err != nil {
		return lightError(err)
	}
	var p pilot
	if err := json.Unmarshal(raw, &p); err != nil {
		return lightError(err)
	}
	if !p.State {
		return "La luz está apagada."
	}
	what := "en una escena"
	switch {
	case p.SceneID > 0 && !whiteScenes[int(p.SceneID)] && sceneNames[int(p.SceneID)] != "":
		what = "en la escena " + sceneNames[int(p.SceneID)]
	case p.R != nil && p.G != nil && p.B != nil:
		what = "en un color"
		for name, c := range lightColors {
			if c == [3]float64{*p.R, *p.G, *p.B} {
				what = "en " + name
			}
		}
	case p.Temp > 0:
		what = "en " + whiteName(p.Temp)
	}
	return fmt.Sprintf("La luz está encendida %s, al %d por ciento.", what, int(p.Dimming))
}

// setLight turns the bulb on or off, sets brightness (10-100), a color name
// or a white tone (warm, neutral, cool).
func setLight(state, color, white, scene, effect string, brightness float64, hasBrightness bool) string {
	if svc.Light == nil {
		return "No hay ningún bombillo configurado."
	}
	svc.Light.StopEffect() // any light command ends a running effect
	if effect != "" && state != "off" {
		if fold(effect) != "aurora" {
			return "Por ahora el único efecto que tengo es la aurora boreal."
		}
		if _, err := svc.Light.call("getPilot", map[string]any{}); err != nil {
			return lightError(err)
		}
		svc.Light.startAurora()
		return "Listo, puse la aurora boreal en la luz."
	}
	if scene != "" && state != "off" {
		id, ok := lightScenes[fold(scene)]
		if !ok {
			return "No conozco esa escena. Puedo poner, por ejemplo, océano, atardecer, chimenea, bosque, fiesta, velas, navidad o cielo nevado."
		}
		ps := map[string]any{"state": true, "sceneId": id}
		if hasBrightness {
			ps["dimming"] = min(max(brightness, 10), 100)
		}
		if _, err := svc.Light.call("setPilot", ps); err != nil {
			return lightError(err)
		}
		return "Listo, puse la escena " + sceneNames[id] + "."
	}
	params := map[string]any{}
	var said []string
	if state == "off" {
		if _, err := svc.Light.call("setPilot", map[string]any{"state": false}); err != nil {
			return lightError(err)
		}
		return "Listo, apagué la luz."
	}
	if color == "blanco" {
		color, white = "", "neutral"
	}
	if color != "" {
		c, ok := lightColors[color]
		if !ok {
			return "No conozco ese color. Puedo poner rojo, naranja, amarillo, verde, azul, morado, rosado o blanco."
		}
		params["r"], params["g"], params["b"] = c[0], c[1], c[2]
		said = append(said, color)
	} else if white != "" {
		t, ok := whiteTemps[white]
		if !ok {
			return "Puedo poner la luz en blanco cálido, neutro o frío."
		}
		params["temp"] = t
		said = append(said, "en "+whiteName(t))
	}
	if hasBrightness {
		b := min(max(brightness, 10), 100)
		params["dimming"] = b
		said = append(said, fmt.Sprintf("al %d por ciento", int(b)))
	}
	params["state"] = true
	if _, err := svc.Light.call("setPilot", params); err != nil {
		return lightError(err)
	}
	if len(said) == 0 {
		return "Listo, prendí la luz."
	}
	out := "Listo, puse la luz"
	for _, s := range said {
		out += " " + s
	}
	return out + "."
}

// lightHelp lists what the bulb can do and how to ask for it.
const lightHelp = "Con el bombillo puedes pedirme: prende la luz o apaga la luz; pon la luz al 50 por ciento, " +
	"o cualquier brillo del 10 al 100; un color, por ejemplo pon la luz azul, roja, verde, amarilla, naranja, morada, rosada o blanca; " +
	"el tono de blanco, pon la luz cálida, neutra o fría; el efecto de aurora boreal, pon la luz de aurora boreal, " +
	"que sigue hasta que me pidas otra cosa de la luz o digas para; y las escenas del bombillo, por ejemplo pon la escena océano, " +
	"atardecer, chimenea, bosque, jungla, velas, fiesta, club, relajación, concentración, luz nocturna, hora de dormir, despertar, " +
	"primavera, verano, otoño, navidad, halloween o cielo nevado. También puedes preguntarme ¿la luz está prendida?"

// isLightHelpQuestion recognizes "¿qué podemos hacer con el bombillo?".
func isLightHelpQuestion(text string) bool {
	f := fold(text)
	asks := false
	for _, a := range []string{"que puedes hacer", "que podemos hacer", "que sabes hacer", "que se puede hacer", "que cosas puedes hacer", "que opciones"} {
		if strings.Contains(f, a) {
			asks = true
		}
	}
	if !asks {
		return false
	}
	for _, k := range []string{"bombillo", "bombilla", "foco", "la luz", "lampara"} {
		if strings.Contains(f, k) {
			return true
		}
	}
	return false
}
