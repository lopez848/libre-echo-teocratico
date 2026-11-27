package hap

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	bhap "github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
)

// startThermostat runs a real HomeKit accessory (brutella/hap) on localhost.
func startThermostat(t *testing.T) (addr string, th *accessory.Thermostat) {
	t.Helper()
	if raceEnabled {
		t.Skip("brutella/dnssd races internally; run without -race")
	}
	th = accessory.NewThermostat(accessory.Info{Name: "Aire acondicionado", Manufacturer: "test"})
	th.Thermostat.CurrentTemperature.SetValue(22.5)
	th.Thermostat.TargetTemperature.SetValue(21)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	s, err := bhap.NewServer(bhap.NewFsStore(filepath.Join(t.TempDir(), "db")), th.A)
	if err != nil {
		t.Fatal(err)
	}
	s.Pin = "12344321"
	s.Addr = "127.0.0.1:" + strconv.Itoa(port)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.ListenAndServe(ctx)
	addr = s.Addr
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr, th
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("accessory did not start")
	return
}

func TestPairVerifyReadAndWrite(t *testing.T) {
	addr, th := startThermostat(t)
	ctrl, err := NewController("mia-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Pair(addr, "000-00-000", ctrl); err == nil {
		t.Fatal("a wrong setup code must fail")
	}
	p, err := Pair(addr, "123-44-321", ctrl)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if p.AccessoryID == "" || len(p.AccessoryLTPK) != 32 {
		t.Fatalf("pairing %+v", p)
	}
	c, err := Dial(addr, p)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer c.Close()
	accs, err := c.Accessories()
	if err != nil {
		t.Fatal(err)
	}
	cur, ok := accs.Find(TypeCurrentTemperature)
	if !ok {
		t.Fatalf("no current temperature in %+v", accs)
	}
	tgt, _ := accs.Find(TypeTargetTemperature)
	vals, err := c.Get(cur, tgt)
	if err != nil {
		t.Fatal(err)
	}
	if vals[cur] != 22.5 || vals[tgt] != 21.0 {
		t.Fatalf("values %v", vals)
	}
	if err := c.Put(map[ID]any{tgt: 23.5}); err != nil {
		t.Fatal(err)
	}
	if got := th.Thermostat.TargetTemperature.Value(); got != 23.5 {
		t.Fatalf("accessory target %v, want 23.5", got)
	}
	// A second session with the stored pairing works without the code.
	c2, err := Dial(addr, p)
	if err != nil {
		t.Fatalf("second verify: %v", err)
	}
	c2.Close()
	// A pairing with the wrong accessory key must be rejected.
	bad := *p
	bad.AccessoryLTPK = make([]byte, 32)
	if c3, err := Dial(addr, &bad); err == nil {
		c3.Close()
		t.Fatal("verify must reject an accessory that cannot prove its key")
	}
}

func TestTLVFragmentsLongValues(t *testing.T) {
	long := make([]byte, 600)
	for i := range long {
		long[i] = byte(i)
	}
	b := encodeTLV(tlvItem{tPublicKey, long}, tlvItem{tState, []byte{2}})
	m, err := decodeTLV(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(m[tPublicKey]) != string(long) || m[tState][0] != 2 {
		t.Fatal("round trip failed")
	}
}
