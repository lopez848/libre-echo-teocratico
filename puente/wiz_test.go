package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBulb answers WiZ UDP requests like a real bulb and records setPilot.
type fakeBulb struct {
	mu    sync.Mutex
	pilot map[string]any
	pc    net.PacketConn
	n     int // setPilot requests received
}

func newFakeBulb(t *testing.T) *fakeBulb {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBulb{pc: pc, pilot: map[string]any{"mac": "6c2990fd0056", "state": true, "sceneId": 11.0, "temp": 2700.0, "dimming": 100.0}}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var req struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			json.Unmarshal(buf[:n], &req)
			b.mu.Lock()
			var out []byte
			switch req.Method {
			case "getPilot":
				out, _ = json.Marshal(map[string]any{"method": "getPilot", "env": "pro", "result": b.pilot})
			case "setPilot":
				b.n++
				if _, ok := req.Params["sceneId"]; ok {
					delete(b.pilot, "r")
					delete(b.pilot, "temp")
				}
				for k, v := range req.Params {
					b.pilot[k] = v
				}
				if _, ok := req.Params["r"]; ok {
					delete(b.pilot, "temp")
					delete(b.pilot, "sceneId")
				}
				if _, ok := req.Params["temp"]; ok {
					delete(b.pilot, "r")
					delete(b.pilot, "sceneId")
				}
				out, _ = json.Marshal(map[string]any{"method": "setPilot", "env": "pro", "result": map[string]any{"success": true}})
			}
			b.mu.Unlock()
			pc.WriteTo(out, from)
		}
	}()
	return b
}

func (b *fakeBulb) get(k string) any { b.mu.Lock(); defer b.mu.Unlock(); return b.pilot[k] }

func withBulb(t *testing.T) *fakeBulb {
	b := newFakeBulb(t)
	old := svc.Light
	svc.Light = &Wiz{Broadcast: b.pc.LocalAddr().String()}
	t.Cleanup(func() { svc.Light = old })
	return b
}

func TestLightStatus(t *testing.T) {
	withBulb(t)
	if got := Exec(&fakeAPI{}, "light_status", `{}`); got != "La luz está encendida en blanco cálido, al 100 por ciento." {
		t.Fatalf("reply %q", got)
	}
}

func TestSetLight(t *testing.T) {
	b := withBulb(t)
	cases := []struct {
		args, reply string
		check       func() bool
	}{
		{`{"state":"off"}`, "Listo, apagué la luz.", func() bool { return b.get("state") == false }},
		{`{"state":"on"}`, "Listo, prendí la luz.", func() bool { return b.get("state") == true }},
		{`{"brightness":40}`, "Listo, puse la luz al 40 por ciento.", func() bool { return b.get("dimming") == 40.0 }},
		{`{"color":"azul"}`, "Listo, puse la luz azul.", func() bool { return b.get("r") == 0.0 && b.get("b") == 255.0 }},
		{`{"white":"cool"}`, "Listo, puse la luz en blanco frío.", func() bool { return b.get("temp") == 6500.0 }},
		{`{"color":"verde","brightness":5}`, "Listo, puse la luz verde al 10 por ciento.", func() bool { return b.get("g") == 255.0 && b.get("dimming") == 10.0 }},
		{`{"color":"plateado"}`, "No conozco ese color. Puedo poner rojo, naranja, amarillo, verde, azul, morado, rosado o blanco.", func() bool { return true }},
	}
	for _, c := range cases {
		if got := Exec(&fakeAPI{}, "set_light", c.args); got != c.reply || !c.check() {
			t.Errorf("%s -> %q (pilot ok=%v)", c.args, got, c.check())
		}
	}
	if got := Exec(&fakeAPI{}, "light_status", `{}`); got != "La luz está encendida en verde, al 10 por ciento." {
		t.Fatalf("status after color %q", got)
	}
}

func TestLightNotFound(t *testing.T) {
	pc, _ := net.ListenPacket("udp4", "127.0.0.1:0") // nothing answers
	defer pc.Close()
	old := svc.Light
	svc.Light = &Wiz{Broadcast: pc.LocalAddr().String()}
	defer func() { svc.Light = old }()
	if got := Exec(&fakeAPI{}, "light_status", `{}`); got != "No encontré el bombillo en la red." {
		t.Fatalf("reply %q", got)
	}
}

func (b *fakeBulb) sets() int { b.mu.Lock(); defer b.mu.Unlock(); return b.n }

func TestLightScenes(t *testing.T) {
	b := withBulb(t)
	if got := Exec(&fakeAPI{}, "set_light", `{"scene":"Océano"}`); got != "Listo, puse la escena océano." || b.get("sceneId") != 1.0 {
		t.Fatalf("reply %q scene %v", got, b.get("sceneId"))
	}
	if got := Exec(&fakeAPI{}, "set_light", `{"scene":"cielo nevado"}`); got != "Listo, puse la escena cielo nevado." || b.get("sceneId") != 36.0 {
		t.Fatalf("reply %q scene %v", got, b.get("sceneId"))
	}
	if got := Exec(&fakeAPI{}, "set_light", `{"scene":"discoteca lunar"}`); !strings.HasPrefix(got, "No conozco esa escena.") {
		t.Fatalf("unknown scene %q", got)
	}
	if got := Exec(&fakeAPI{}, "light_status", `{}`); got != "La luz está encendida en la escena cielo nevado, al 100 por ciento." {
		t.Fatalf("status %q", got)
	}
}

// The aurora is the bridge's own effect: slow moves among greens, teals and
// violets until another light command (owner, 2026-11-26).
func TestAuroraEffectRunsUntilAnotherCommand(t *testing.T) {
	b := withBulb(t)
	svc.Light.EffectStep = 5 * time.Millisecond
	if got := Exec(&fakeAPI{}, "set_light", `{"effect":"aurora"}`); got != "Listo, puse la aurora boreal en la luz." {
		t.Fatalf("reply %q", got)
	}
	waitFor(t, func() bool { return b.sets() >= 20 })
	r, g, bl := b.get("r").(float64), b.get("g").(float64), b.get("b").(float64)
	if r > g && r > bl {
		t.Fatalf("aurora colors are greens, teals and violets, got r=%v g=%v b=%v", r, g, bl)
	}
	if got := Exec(&fakeAPI{}, "set_light", `{"state":"off"}`); got != "Listo, apagué la luz." {
		t.Fatalf("off %q", got)
	}
	n := b.sets()
	time.Sleep(40 * time.Millisecond)
	if b.sets() != n || b.get("state") != false {
		t.Fatalf("the effect must stop with another light command: %d -> %d, state %v", n, b.sets(), b.get("state"))
	}
}

func TestStopWordStopsTheAurora(t *testing.T) {
	b := withBulb(t)
	svc.Light.EffectStep = 5 * time.Millisecond
	Exec(&fakeAPI{}, "set_light", `{"effect":"aurora"}`)
	waitFor(t, func() bool { return b.sets() >= 5 })
	s := &server{api: &fakeAPI{}}
	s.answer(context.Background(), inbound{Messages: []chatMessage{{Role: "user", Content: "para"}}}, "")
	n := b.sets()
	time.Sleep(40 * time.Millisecond)
	if b.sets() != n {
		t.Fatal("\"para\" must stop the aurora")
	}
}

func TestLightHelp(t *testing.T) {
	got := Exec(&fakeAPI{}, "light_help", `{}`)
	for _, want := range []string{"prende la luz", "apaga la luz", "al 50", "azul", "cálida", "aurora boreal", "escena océano", "cielo nevado", "¿la luz está prendida?"} {
		if !strings.Contains(got, want) {
			t.Errorf("help must mention %q: %s", want, got)
		}
	}
	for _, q := range []string{"¿Qué podemos hacer con el bombillo?", "qué puedes hacer con la luz", "qué sabes hacer con el foco"} {
		if !isLightHelpQuestion(q) {
			t.Errorf("%q is the light help question", q)
		}
	}
	if isLightHelpQuestion("qué puedes hacer con jw.org") || isLightHelpQuestion("prende la luz") {
		t.Fatal("other requests are not the light help")
	}
}
