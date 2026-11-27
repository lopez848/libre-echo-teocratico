package main

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
)

// fakeBulb answers WiZ UDP requests like a real bulb and records setPilot.
type fakeBulb struct {
	mu    sync.Mutex
	pilot map[string]any
	pc    net.PacketConn
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
