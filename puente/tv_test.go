package main

import (
	"os"
	"strings"
	"testing"
)

// tvsWithFake points svc.TVs at a fake adbd and restores the old value.
func tvsWithFake(t *testing.T, f *fakeAdbd, names ...string) {
	t.Helper()
	dir := t.TempDir()
	items := make([]TV, 0, len(names))
	for i, n := range names {
		items = append(items, TV{Name: n, Host: f.addr()})
		_ = i
	}
	old := svc.TVs
	tvs := &TVs{KeyDir: dir, items: items}
	svc.TVs = tvs
	t.Cleanup(func() { svc.TVs = old })
}

func TestTVsPickHost(t *testing.T) {
	sala := TV{Name: "Sala", Host: "192.168.1.247"}
	hab := TV{Name: "Habitacion", Host: "192.168.1.233"}
	one := &TVs{items: []TV{sala}}
	two := &TVs{items: []TV{sala, hab}}

	if got, ok := one.pickHost(""); !ok || got.Host != sala.Host {
		t.Fatalf("single tv with no name = %+v %v", got, ok)
	}
	if got, ok := one.pickHost("la sala"); !ok || got.Host != sala.Host {
		t.Fatalf("single tv named = %+v %v", got, ok)
	}
	if _, ok := two.pickHost(""); ok {
		t.Fatal("two tvs with no name must be ambiguous")
	}
	if _, ok := two.pickHost("tv"); ok {
		t.Fatal("bare 'tv' must be ambiguous")
	}
	if got, ok := two.pickHost("la sala"); !ok || got.Name != sala.Name {
		t.Fatalf("sala pick = %+v %v", got, ok)
	}
	if got, ok := two.pickHost("el de la habitación"); !ok || got.Name != hab.Name {
		t.Fatalf("habitacion pick = %+v %v", got, ok)
	}
	if _, ok := two.pickHost("la cocina"); ok {
		t.Fatal("unknown room must fail")
	}
}

func TestTVControlOpenApp(t *testing.T) {
	f := newFakeAdbd(t, false)
	f.shellOut = func(cmd string, out func(string)) {
		switch {
		case cmd == "true":
		case strings.HasPrefix(cmd, "pm list packages"):
			out("package:com.example.thing\npackage:com.netflix.ninja\n")
		case strings.HasPrefix(cmd, "monkey -p "):
		case strings.HasPrefix(cmd, "am start -n "):
		}
	}
	tvsWithFake(t, f, "Sala")
	got := tvControl("abrir", "", "netflix", "", "", 0)
	if !strings.Contains(got, "Abriendo netflix en el TV de Sala") {
		t.Fatalf("open answer = %q", got)
	}
	served := strings.Join(f.served, "; ")
	if !strings.Contains(served, "monkey -p com.netflix.ninja") {
		t.Fatalf("open did not launch netflix: %v", f.served)
	}
}

func TestTVControlClosesWithForceStop(t *testing.T) {
	f := newFakeAdbd(t, false)
	f.shellOut = func(cmd string, out func(string)) {
		if strings.HasPrefix(cmd, "pm list packages") {
			out("package:com.google.android.youtube.tv\n")
		}
	}
	tvsWithFake(t, f, "Sala")
	got := tvControl("cerrar", "", "youtube", "", "", 0)
	if !strings.Contains(got, "Cerrando youtube en el TV de Sala") {
		t.Fatalf("close answer = %q", got)
	}
	if !strings.Contains(strings.Join(f.served, "; "), "am force-stop com.google.android.youtube.tv") {
		t.Fatalf("close did not force-stop: %v", f.served)
	}
}

func TestTVControlAmbiguousQuestion(t *testing.T) {
	f := newFakeAdbd(t, false)
	conn := t.TempDir()
	old := svc.TVs
	svc.TVs = &TVs{KeyDir: conn, items: []TV{{Name: "Sala", Host: f.addr()}, {Name: "Habitación", Host: f.addr()}}}
	t.Cleanup(func() { svc.TVs = old })
	got := tvControl("tecla", "", "", "pausa", "", 0)
	if !strings.Contains(got, "¿En cuál TV?") || !strings.Contains(got, "Sala") || !strings.Contains(got, "Habitación") {
		t.Fatalf("ambiguous answer = %q", got)
	}
}

func TestTVControlUnreachable(t *testing.T) {
	old := svc.TVs
	svc.TVs = &TVs{KeyDir: t.TempDir(), items: []TV{{Name: "Sala", Host: "127.0.0.1:9"}}}
	t.Cleanup(func() { svc.TVs = old })
	got := tvControl("tecla", "", "", "pausa", "", 0)
	if !strings.Contains(got, "No alcanzo el TV de Sala") {
		t.Fatalf("unreachable answer = %q", got)
	}
}

func TestTVControlVolumeSendsKeys(t *testing.T) {
	f := newFakeAdbd(t, false)
	tvsWithFake(t, f, "Sala")
	tvControl("volumen", "la sala", "", "", "subir", 3)
	joined := strings.Join(f.served, "; ")
	if strings.Count(joined, "KEYCODE_VOLUME_UP") != 3 {
		t.Fatalf("volume up keys = %q", joined)
	}
	got := tvControl("volumen", "", "", "", "silenciar", 0)
	if got != "Listo." || !strings.Contains(strings.Join(f.served, "; "), "KEYCODE_VOLUME_MUTE") {
		t.Fatalf("mute answer = %q served %v", got, f.served)
	}
}

func TestTVControlKeyPower(t *testing.T) {
	f := newFakeAdbd(t, false)
	tvsWithFake(t, f, "Sala")
	if got := tvControl("tecla", "", "", "apagar", "", 0); got != "Listo." {
		t.Fatalf("sleep answer = %q", got)
	}
	if got := tvControl("tecla", "", "", "prender", "", 0); got != "Listo." {
		t.Fatalf("wakeup answer = %q", got)
	}
	joined := strings.Join(f.served, "; ")
	if !strings.Contains(joined, "KEYCODE_SLEEP") || !strings.Contains(joined, "KEYCODE_WAKEUP") {
		t.Fatalf("power keys = %q", joined)
	}
}

func TestTVControlUnknownKeyAndAction(t *testing.T) {
	f := newFakeAdbd(t, false)
	tvsWithFake(t, f, "Sala")
	if got := tvControl("tecla", "", "", "cambiar de canal", "", 0); got != "No sé enviar esa tecla." {
		t.Fatalf("unknown key = %q", got)
	}
	if got := tvControl("pintar", "", "", "", "", 0); got != "Todavía no sé hacer eso con el TV." {
		t.Fatalf("unknown action = %q", got)
	}
}

func TestTVControlNoTVs(t *testing.T) {
	old := svc.TVs
	svc.TVs = nil
	t.Cleanup(func() { svc.TVs = old })
	if got := tvControl("abrir", "", "netflix", "", "", 0); got != "No tengo ningún TV configurado." {
		t.Fatalf("no tv answer = %q", got)
	}
}

func TestLoadTVs(t *testing.T) {
	dir := t.TempDir()
	missing := dir + "/nada.json"
	tvs, err := loadTVs(missing)
	if err != nil || tvs != nil {
		t.Fatalf("missing file = %+v %v, want nil nil", tvs, err)
	}
	path := dir + "/tvs.json"
	if err := os.WriteFile(path, []byte(`[{"name":"Sala","host":"192.168.1.247"},{"name":"Habitación","host":"192.168.1.233","apps":{"jw":"org.jw.jwlibrary.mobile"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	tvs, err = loadTVs(path)
	if err != nil || tvs == nil || len(tvs.all()) != 2 {
		t.Fatalf("loadTVs = %+v %v", tvs, err)
	}
	bad := dir + "/tvs.bad"
	if err := os.WriteFile(bad, []byte(`[{"name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTVs(bad); err == nil {
		t.Fatal("malformed json must error")
	}
}
