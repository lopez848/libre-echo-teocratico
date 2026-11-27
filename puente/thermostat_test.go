package main

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	bhap "github.com/brutella/hap"
	"github.com/brutella/hap/accessory"

	"puente/hap"
)

// withThermostat pairs the bridge with a real HomeKit thermostat
// (brutella/hap) shown in Fahrenheit, like the owner's ecobee.
func withThermostat(t *testing.T) *accessory.Thermostat {
	t.Helper()
	if raceEnabled {
		t.Skip("brutella/dnssd races internally; run without -race")
	}
	th := accessory.NewThermostat(accessory.Info{Name: "Aire acondicionado"})
	th.Thermostat.TemperatureDisplayUnits.SetValue(1)
	th.Thermostat.CurrentTemperature.SetValue(22.2) // 72 °F
	th.Thermostat.TargetTemperature.SetValue(21.1)  // 70 °F
	th.Thermostat.TargetHeatingCoolingState.SetValue(2)
	th.Thermostat.CurrentHeatingCoolingState.SetValue(2)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := "127.0.0.1:" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	s, err := bhap.NewServer(bhap.NewFsStore(filepath.Join(t.TempDir(), "db")), th.A)
	if err != nil {
		t.Fatal(err)
	}
	s.Pin, s.Addr = "11122333", addr
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.ListenAndServe(ctx)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	file := filepath.Join(t.TempDir(), "thermostat.json")
	if err := PairThermostat(file, addr, "111-22-333"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Fatalf("pairing file mode %v, want 600", st.Mode().Perm())
	}
	old := svc.Thermo
	svc.Thermo = &Thermostat{File: file}
	t.Cleanup(func() { svc.Thermo.Close(); svc.Thermo = old })
	return th
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.06 }

func TestThermostatStatus(t *testing.T) {
	withThermostat(t)
	got := Exec(&fakeAPI{}, "thermostat_status", `{}`)
	if got != "En la casa hay 72 grados. El aire acondicionado está puesto a 70 grados y está enfriando." {
		t.Fatalf("reply %q", got)
	}
}

func TestSetThermostatTemperatureAndMode(t *testing.T) {
	th := withThermostat(t)
	if got := Exec(&fakeAPI{}, "set_thermostat", `{"temperature":68}`); got != "Listo, puse el aire acondicionado a 68 grados." {
		t.Fatalf("reply %q", got)
	}
	if v := th.Thermostat.TargetTemperature.Value(); !near(v, 20.0) {
		t.Fatalf("target %v °C, want 20.0 (68 °F)", v)
	}
	if got := Exec(&fakeAPI{}, "set_thermostat", `{"mode":"heat","temperature":71}`); got != "Listo, puse la calefacción a 71 grados." {
		t.Fatalf("reply %q", got)
	}
	if th.Thermostat.TargetHeatingCoolingState.Value() != 1 || !near(th.Thermostat.TargetTemperature.Value(), 21.7) {
		t.Fatalf("mode %v target %v", th.Thermostat.TargetHeatingCoolingState.Value(), th.Thermostat.TargetTemperature.Value())
	}
	if got := Exec(&fakeAPI{}, "set_thermostat", `{"mode":"off"}`); got != "Listo, apagué el termostato." {
		t.Fatalf("reply %q", got)
	}
	if th.Thermostat.TargetHeatingCoolingState.Value() != 0 {
		t.Fatal("mode must be off")
	}
	if got := Exec(&fakeAPI{}, "set_thermostat", `{"temperature":120}`); got != "Esa temperatura está fuera de rango: dime una entre 50 y 90 grados." {
		t.Fatalf("out of range %q", got)
	}
}

func TestThermostatNotPaired(t *testing.T) {
	old := svc.Thermo
	svc.Thermo = &Thermostat{File: filepath.Join(t.TempDir(), "missing.json")}
	defer func() { svc.Thermo = old }()
	if got := Exec(&fakeAPI{}, "thermostat_status", `{}`); got != "El termostato todavía no está vinculado." {
		t.Fatalf("reply %q", got)
	}
}

var _ = hap.FormatCode

func TestIndoorTemperatureQuestionSkipsTheModel(t *testing.T) {
	for _, q := range []string{"¿Qué temperatura hace en la casa?", "qué temperatura hay adentro", "a cuánto está el termostato", "cómo está el termostato", "¿está prendido el aire?", "qué temperatura hace en el apartamento"} {
		if !isThermostatQuestion(q) {
			t.Errorf("%q must go to the thermostat", q)
		}
	}
	for _, q := range []string{"qué temperatura hace afuera", "qué tiempo hace", "pon el aire a 72", "apaga el aire", "qué temperatura hace hoy"} {
		if isThermostatQuestion(q) {
			t.Errorf("%q is not an indoor status question", q)
		}
	}
}

// When the router gives the thermostat a new address, the bridge finds it
// on the network by its HomeKit id and saves the new address (owner,
// 2026-11-26: "haz que Mia lo busque sola en la red").
func TestThermostatFindsANewAddress(t *testing.T) {
	withThermostat(t)
	b, _ := os.ReadFile(svc.Thermo.File)
	var f thermostatFile
	json.Unmarshal(b, &f)
	real := f.Addr
	f.Addr = "127.0.0.1:1" // old address, nothing listens there
	b, _ = json.Marshal(f)
	os.WriteFile(svc.Thermo.File, b, 0o600)
	var asked string
	svc.Thermo.Lookup = func(id string) (string, error) { asked = id; return real, nil }
	if got := Exec(&fakeAPI{}, "thermostat_status", `{}`); !strings.HasPrefix(got, "En la casa hay 72 grados.") {
		t.Fatalf("reply %q", got)
	}
	if asked != f.Pairing.AccessoryID {
		t.Fatalf("looked up %q, want the accessory id %q", asked, f.Pairing.AccessoryID)
	}
	b, _ = os.ReadFile(svc.Thermo.File)
	json.Unmarshal(b, &f)
	if f.Addr != real {
		t.Fatalf("saved addr %q, want %q", f.Addr, real)
	}
	if st, _ := os.Stat(svc.Thermo.File); st.Mode().Perm() != 0o600 {
		t.Fatal("the pairing file must stay 600")
	}
}

// If the pairing cannot be saved, the error says how to recover: the
// thermostat is paired to keys that are gone (review T5).
func TestPairingSaveFailureExplainsTheRecovery(t *testing.T) {
	if raceEnabled {
		t.Skip("brutella/dnssd races internally; run without -race")
	}
	th := accessory.NewThermostat(accessory.Info{Name: "x"})
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	s, _ := bhap.NewServer(bhap.NewFsStore(filepath.Join(t.TempDir(), "db")), th.A)
	s.Pin, s.Addr = "11122333", addr
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ListenAndServe(ctx)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	err := PairThermostat(filepath.Join(t.TempDir(), "missing-dir", "thermostat.json"), addr, "11122333")
	if err == nil || !strings.Contains(err.Error(), "reset HomeKit on the thermostat") {
		t.Fatalf("err = %v", err)
	}
}
