package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// TV names one controllable television: an Android TV that takes ADB
// (TCL or Google TV, Fire TV).
type TV struct {
	Name string `json:"name"` // spoken name, e.g. "Sala"
	Host string `json:"host"` // IP address or host:port (default port 5555)
	Apps map[string]string `json:",omitempty"` // app overrides by fold name
}

// TVs is every TV the bridge may control; a nil group disables the tools.
type TVs struct {
	KeyDir string // directory holding "adbkey"
	items  []TV
}

// loadTVs reads the TVs file; a missing file disables the feature and an
// unreadable or malformed one is an error the caller reports once.
func loadTVs(file string) (*TVs, error) {
	if file == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []TV
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("tv config: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &TVs{items: items}, nil
}

func (t *TVs) all() []TV {
	if t == nil {
		return nil
	}
	return t.items
}

// pickHost resolves the user's spoken tv name ("" = no name) to a host.
// With one TV only, no name means it. With several, names like "sala" must
// select exactly one and "" fails as ambiguous.
func (t *TVs) pickHost(spoken string) (TV, bool) {
	all := t.all()
	if len(all) == 1 && fold(spoken) == "" {
		return all[0], true
	}
	q := fold(spoken)
	if q == "" || q == "tv" || q == "el tv" {
		return TV{}, false
	}
	for _, tv := range all {
		if strings.Contains(fold(tv.Name), q) || strings.Contains(q, fold(tv.Name)) {
			return tv, true
		}
	}
	return TV{}, false
}

// names returns the spoken names, for an ambiguity question.
func (t *TVs) names() []string {
	var out []string
	for _, tv := range t.all() {
		out = append(out, tv.Name)
	}
	return out
}

func (tv TV) addr() string {
	if strings.Contains(tv.Host, ":") {
		return tv.Host
	}
	return tv.Host + ":5555"
}

// connect opens an ADB connection to the TV.
func (t *TVs) connect(tv TV) (*adbConn, error) {
	if t.KeyDir == "" {
		return nil, errors.New("tv: no key directory")
	}
	key, err := loadADBKey(t.KeyDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return connectADB(ctx, tv.addr(), key)
}

// run is one shell command over a fresh connection.
func (t *TVs) run(tv TV, command string) (string, error) {
	c, err := t.connect(tv)
	if err != nil {
		return "", err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.shell(ctx, command)
}

// tvKeys maps spoken key names to Android key events.
var tvKeys = map[string]string{
	"home":      "KEYCODE_HOME",
	"atras":     "KEYCODE_BACK",
	"arriba":    "KEYCODE_DPAD_UP",
	"abajo":     "KEYCODE_DPAD_DOWN",
	"izquierda": "KEYCODE_DPAD_LEFT",
	"derecha":   "KEYCODE_DPAD_RIGHT",
	"ok":        "KEYCODE_DPAD_CENTER",
	"menu":      "KEYCODE_MENU",
	"reproducir": "KEYCODE_MEDIA_PLAY",
	"pausa":     "KEYCODE_MEDIA_PAUSE",
	"siguiente": "KEYCODE_MEDIA_NEXT",
	"anterior":  "KEYCODE_MEDIA_PREVIOUS",
	"silenciar": "KEYCODE_VOLUME_MUTE",
	"prender":   "KEYCODE_WAKEUP",
	"apagar":    "KEYCODE_SLEEP",
}

// tvApps maps Spanish app names to Android TV packages.
var tvApps = map[string]string{
	"jw":        "org.jw.jwlibrary.mobile",
	"netflix":   "com.netflix.ninja",
	"youtube":   "com.google.android.youtube.tv",
	"spotify":   "com.spotify.tv.android",
	"disney":    "com.disney.disneyplus",
	"max":       "com.hbo.hbomax",
	"amazon":    "com.amazon.vodka.livingroom.global",
	"prime":     "com.amazon.vodka.livingroom.global",
	"paramount": "com.cbs.ottapp",
	"star":      "com.disney.star_plus",
}

// resolveApp finds the package of an app by the Spanish fold name: it
// prefers the configured overrides, then the map above, then the packages
// the TV reports whose name contains the fragment.
func (t *TVs) resolveApp(tv TV, app string) (string, error) {
	q := fold(app)
	if p, ok := tv.Apps[q]; ok && p != "" {
		return p, nil
	}
	if p, ok := tvApps[q]; ok {
		return p, nil
	}
	if q == "" {
		return "", errors.New("app vacía")
	}
	out, err := t.run(tv, "pm list packages -3")
	if err != nil {
		return "", err
	}
	var found []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "package:"))
		if line == "" {
			continue
		}
		if strings.Contains(fold(line), q) {
			found = append(found, strings.TrimPrefix(strings.TrimSpace(line), "package:"))
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no encuentro la app %q en el TV", app)
	case 1:
		return found[0], nil
	default:
		sort.Strings(found)
		return "", fmt.Errorf("la app %q puede ser %s", app, strings.Join(found, ", "))
	}
}

// tvActionHelp is the spoken answer explaining what the TV tools do.
var tvHelp = `Los TV se controlan por voz: abrí o cerrá aplicaciones, pausá o ` +
	`reanudad el video, movete por el menú, subí y bajá el volumen, ` +
	`prendé o apagá la pantalla. Por ejemplo, "poné Netflix en el TV" o "pausá el TV".`

// tvControl runs the spoken tv command and answers in neutral Spanish.
// action: abrir or cerrar an app (app), tecla sends a key (key), volumen
// moves or mutes (volume, steps). tv names one TV; empty picks it when
// there is only one and asks when there are several.
func tvControl(action, tv, app, key, volume string, steps int) string {
	if svc.TVs == nil {
		return "No tengo ningún TV configurado."
	}
	name, ok := svc.TVs.pickHost(tv)
	if !ok {
		switch names := svc.TVs.names(); len(names) {
		case 0:
			return "No tengo ningún TV configurado."
		case 1:
			name = svc.TVs.all()[0]
		default:
			return "¿En cuál TV? Tengo configurado " + joinES(names) + "."
		}
	}
	if _, err := svc.TVs.run(name, "true"); err != nil {
		return spokenReachable(err, name.Name)
	}
	switch fold(action) {
	case "abrir":
		pkg, err := svc.TVs.resolveApp(name, app)
		if err != nil {
			if strings.Contains(err.Error(), "puede ser") {
				return strings.TrimSuffix(err.Error(), ".") + ". ¿Cuál quiero?"
			}
			return "No logré abrir " + app + " en el TV de " + name.Name + "."
		}
		if _, err := svc.TVs.run(name, "monkey -p "+pkg+" -c android.intent.category.LAUNCHER 1"); err != nil {
			if _, err2 := svc.TVs.run(name, "am start -n "+pkg); err2 != nil {
				return "No logré abrir " + app + " en el TV de " + name.Name + "."
			}
		}
		return "Abriendo " + app + " en el TV de " + name.Name + "."
	case "cerrar":
		pkg, err := svc.TVs.resolveApp(name, app)
		if err != nil {
			return "No logré cerrar " + app + " en el TV de " + name.Name + "."
		}
		if _, err := svc.TVs.run(name, "am force-stop "+pkg); err != nil {
			return "No logré cerrar " + app + " en el TV de " + name.Name + "."
		}
		return "Cerrando " + app + " en el TV de " + name.Name + "."
	case "tecla":
		code, ok := tvKeys[fold(key)]
		if !ok {
			return "No sé enviar esa tecla."
		}
		if _, err := svc.TVs.run(name, "input keyevent "+code); err != nil {
			return spokenReachable(err, name.Name)
		}
		return "Listo."
	case "volumen":
		var code string
		switch fold(volume) {
		case "subir":
			code = "KEYCODE_VOLUME_UP"
			if steps < 1 {
				steps = 1
			}
		case "bajar":
			code = "KEYCODE_VOLUME_DOWN"
			if steps < 1 {
				steps = 1
			}
		case "silenciar":
			code = "KEYCODE_VOLUME_MUTE"
			steps = 1
		default:
			return "No sé hacer ese movimiento de volumen."
		}
		evt := "input keyevent " + code
		for ; steps > 1; steps-- {
			evt += " && input keyevent " + code
		}
		if _, err := svc.TVs.run(name, evt); err != nil {
			return spokenReachable(err, name.Name)
		}
		return "Listo."
	default:
		return "Todavía no sé hacer eso con el TV."
	}
}

// spokenReachable explains a TV that could not be reached.
func spokenReachable(err error, tv string) string {
	switch {
	case errors.Is(err, errADBPairing):
		return "No alcanzo el TV de " + tv + ": acepta el aviso de depuración que aparece en su pantalla y vuelve a pedirmelo."
	case strings.Contains(err.Error(), "no key directory"):
		return "Todavía no tengo la clave para hablar con los TV."
	default:
		return "No alcanzo el TV de " + tv + "."
	}
}
