package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Wiz controls a WiZ (Philips) bulb through its local UDP API on port
// 38899. The bulb is found by broadcasting getPilot, so a new address from
// the router does not matter.
type Wiz struct {
	Broadcast string // where discovery goes; default 255.255.255.255:38899

	mu   sync.Mutex
	addr string // last address the bulb answered from
}

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
func setLight(state, color, white string, brightness float64, hasBrightness bool) string {
	if svc.Light == nil {
		return "No hay ningún bombillo configurado."
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
