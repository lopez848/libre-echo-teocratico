package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"puente/hap"
)

// thermostatFile is what PairThermostat saves: the HomeKit pairing (it
// grants control of the thermostat, so the file is mode 600) and address.
type thermostatFile struct {
	Addr    string       `json:"addr"`
	Pairing *hap.Pairing `json:"pairing"`
}

// PairThermostat pairs with a HomeKit thermostat using its 8-digit setup
// code and saves the pairing. The code is never logged or stored.
func PairThermostat(file, addr, code string) error {
	ctrl, err := hap.NewController("libre-echo-mia")
	if err != nil {
		return err
	}
	p, err := hap.Pair(addr, code, ctrl)
	if err != nil {
		return err
	}
	if err := saveThermostat(file, thermostatFile{Addr: addr, Pairing: p}); err != nil {
		// HomeKit allows one controller: the thermostat is now paired with
		// keys that are lost.
		return fmt.Errorf("paired, but the pairing could not be saved (%v); reset HomeKit on the thermostat (Settings > Reset > HomeKit) and pair again", err)
	}
	return nil
}

func saveThermostat(file string, f thermostatFile) error {
	b, _ := json.Marshal(f)
	tmp := file + ".tmp"
	w, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	if err == nil {
		err = w.Sync() // the keys must be on disk before the rename
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}

// Thermostat talks to the paired thermostat, keeping one session open.
type Thermostat struct {
	File string
	// Lookup finds the thermostat's current address by its HomeKit id when
	// the saved one stops answering (nil: mDNS on the local network).
	Lookup func(id string) (string, error)

	mu     sync.Mutex
	client *hap.Client
	ids    map[string]hap.ID
}

var errNotPaired = errors.New("thermostat not paired")

func (t *Thermostat) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		t.client.Close()
		t.client = nil
	}
}

func (t *Thermostat) connect() error {
	if t.client != nil {
		return nil
	}
	b, err := os.ReadFile(t.File)
	if os.IsNotExist(err) {
		return errNotPaired
	}
	if err != nil {
		return err
	}
	var f thermostatFile
	if err := json.Unmarshal(b, &f); err != nil || f.Pairing == nil {
		return fmt.Errorf("thermostat file: %v", err)
	}
	c, err := hap.Dial(f.Addr, f.Pairing)
	if err != nil {
		addr, lerr := t.lookup(f.Pairing.AccessoryID)
		if lerr != nil || addr == f.Addr {
			return err
		}
		c, err = hap.Dial(addr, f.Pairing)
		if err != nil {
			return err
		}
		logf("thermostat moved from %s to %s", f.Addr, addr)
		f.Addr = addr
		if serr := saveThermostat(t.File, f); serr != nil {
			logf("thermostat: could not save the new address: %v", serr)
		}
	}
	accs, err := c.Accessories()
	if err != nil {
		c.Close()
		return err
	}
	ids := map[string]hap.ID{}
	for _, typ := range []string{hap.TypeCurrentTemperature, hap.TypeTargetTemperature, hap.TypeTargetHeatingCoolingState,
		hap.TypeCurrentHeatingCoolingState, hap.TypeTemperatureDisplayUnits} {
		id, ok := accs.Find(typ)
		if !ok {
			c.Close()
			return fmt.Errorf("thermostat has no characteristic %s", typ)
		}
		ids[typ] = id
	}
	t.client, t.ids = c, ids
	return nil
}

func (t *Thermostat) lookup(id string) (string, error) {
	if t.Lookup != nil {
		return t.Lookup(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return hap.Lookup(ctx, hap.MDNSGroup, id)
}

// with runs fn on a session, reconnecting once if the session broke.
func (t *Thermostat) with(fn func(c *hap.Client, ids map[string]hap.ID) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for attempt := 0; ; attempt++ {
		if err := t.connect(); err != nil {
			return err
		}
		err := fn(t.client, t.ids)
		if err == nil || attempt == 1 {
			return err
		}
		t.client.Close()
		t.client = nil
	}
}

type thermoState struct {
	current, target float64 // °C
	mode, running   int     // target 0 off 1 heat 2 cool 3 auto; current 0 idle 1 heating 2 cooling
	fahrenheit      bool
}

func num(v any) float64 { f, _ := v.(float64); return f }

func (t *Thermostat) state() (thermoState, error) {
	var s thermoState
	err := t.with(func(c *hap.Client, ids map[string]hap.ID) error {
		v, err := c.Get(ids[hap.TypeCurrentTemperature], ids[hap.TypeTargetTemperature], ids[hap.TypeTargetHeatingCoolingState],
			ids[hap.TypeCurrentHeatingCoolingState], ids[hap.TypeTemperatureDisplayUnits])
		if err != nil {
			return err
		}
		s = thermoState{current: num(v[ids[hap.TypeCurrentTemperature]]), target: num(v[ids[hap.TypeTargetTemperature]]),
			mode: int(num(v[ids[hap.TypeTargetHeatingCoolingState]])), running: int(num(v[ids[hap.TypeCurrentHeatingCoolingState]])),
			fahrenheit: num(v[ids[hap.TypeTemperatureDisplayUnits]]) == 1}
		return nil
	})
	return s, err
}

// display converts °C to the thermostat's display unit, rounded.
func (s thermoState) display(c float64) int {
	if s.fahrenheit {
		return int(math.Round(c*9/5 + 32))
	}
	return int(math.Round(c))
}

func modeName(mode int) string {
	switch mode {
	case 1:
		return "la calefacción"
	case 2:
		return "el aire acondicionado"
	case 3:
		return "el modo automático"
	}
	return "el termostato"
}

func thermostatError(err error) string {
	if errors.Is(err, errNotPaired) {
		return "El termostato todavía no está vinculado."
	}
	logf("thermostat: %v", err)
	return "No pude comunicarme con el termostato."
}

func thermostatStatus() string {
	if svc.Thermo == nil {
		return "No hay ningún termostato configurado."
	}
	s, err := svc.Thermo.state()
	if err != nil {
		return thermostatError(err)
	}
	out := fmt.Sprintf("En la casa hay %d grados. ", s.display(s.current))
	switch s.mode {
	case 0:
		return out + "El termostato está apagado."
	case 3:
		return out + "Está en modo automático."
	}
	verb := "puesto"
	if s.mode == 1 {
		verb = "puesta"
	}
	out += fmt.Sprintf("%s está %s a %d grados", capitalize(modeName(s.mode)), verb, s.display(s.target))
	switch {
	case s.running == 1:
		out += " y está calentando"
	case s.running == 2:
		out += " y está enfriando"
	}
	return out + "."
}

var modeValues = map[string]int{"off": 0, "heat": 1, "cool": 2, "auto": 3}

// setThermostat changes the mode and/or the target temperature, given in
// the thermostat's display unit.
func setThermostat(mode string, temp float64, hasTemp bool) string {
	if svc.Thermo == nil {
		return "No hay ningún termostato configurado."
	}
	s, err := svc.Thermo.state()
	if err != nil {
		return thermostatError(err)
	}
	newMode := s.mode
	if mode != "" {
		m, ok := modeValues[mode]
		if !ok {
			return "No conozco ese modo del termostato."
		}
		newMode = m
	}
	var celsius float64
	if hasTemp {
		lo, hi := 10.0, 32.0
		if s.fahrenheit {
			lo, hi = 50, 90
		}
		if temp < lo || temp > hi {
			return fmt.Sprintf("Esa temperatura está fuera de rango: dime una entre %d y %d grados.", int(lo), int(hi))
		}
		if newMode == 3 {
			return "En modo automático no puedo fijar una sola temperatura. Dime si quieres calefacción o aire acondicionado."
		}
		celsius = temp
		if s.fahrenheit {
			celsius = (temp - 32) * 5 / 9
		}
		celsius = math.Round(celsius*10) / 10
	}
	err = svc.Thermo.with(func(c *hap.Client, ids map[string]hap.ID) error {
		vals := map[hap.ID]any{}
		if mode != "" {
			vals[ids[hap.TypeTargetHeatingCoolingState]] = newMode
		}
		if hasTemp {
			vals[ids[hap.TypeTargetTemperature]] = celsius
		}
		return c.Put(vals)
	})
	if err != nil {
		return thermostatError(err)
	}
	switch {
	case newMode == 0:
		if hasTemp {
			return fmt.Sprintf("Listo, lo dejé en %d grados, pero el termostato está apagado. Dime si quieres calefacción o aire acondicionado.", int(math.Round(temp)))
		}
		return "Listo, apagué el termostato."
	case newMode == 3:
		return "Listo, puse el termostato en modo automático."
	case hasTemp:
		return fmt.Sprintf("Listo, puse %s a %d grados.", modeName(newMode), int(math.Round(temp)))
	default:
		return fmt.Sprintf("Listo, encendí %s.", modeName(newMode))
	}
}

// isThermostatQuestion recognizes questions about the indoor temperature or
// the thermostat, answered from the thermostat without the model (the model
// tends to answer with the outdoor weather that agentd adds to the prompt).
func isThermostatQuestion(text string) bool {
	f := fold(text)
	for _, w := range []string{"afuera", "fuera", "exterior", "pon ", "apaga", "prende el", "enciende", "sube", "baja"} {
		if strings.Contains(f, w) {
			return false
		}
	}
	if strings.Contains(f, "termostato") {
		return true
	}
	if strings.Contains(f, "esta prendido el aire") || strings.Contains(f, "esta encendido el aire") ||
		strings.Contains(f, "esta prendida la calefaccion") || strings.Contains(f, "esta encendida la calefaccion") {
		return true
	}
	if strings.Contains(f, "temperatura") {
		for _, w := range []string{"en la casa", "adentro", "dentro", "en el apartamento", "en el apto", "en el departamento"} {
			if strings.Contains(f, w) {
				return true
			}
		}
	}
	return false
}
