package main

// Spoken controls handled in the bridge without the model:
// "para" / "stop" end everything (no resume), "pausa" pauses the reading or
// the music keeping the place, "reanudar" / "continuar" go on from there.

var pauseWords = map[string]bool{
	"pausa": true, "pausar": true, "pon pausa": true, "ponle pausa": true, "pausalo": true, "pausala": true,
	"pausa la lectura": true, "pausa la musica": true, "en pausa": true,
}

var resumeWords = map[string]bool{
	"reanudar": true, "reanuda": true, "reanudalo": true, "reanudala": true, "continuar": true, "continua": true,
	"continua la lectura": true, "continuar la lectura": true, "sigue": true, "seguir": true, "sigue leyendo": true,
	"reanuda la musica": true, "reanuda la lectura": true, "quita la pausa": true,
}

var (
	pausedPlayer bool // the music was paused with "pausa"
	turnReading  bool // a reading was being spoken when this turn started
)

func isControl(text string) bool {
	f := fold(text)
	return stopWords[f] || pauseWords[f] || resumeWords[f]
}

func pauseAll() string {
	if svc.Reader != nil && (turnReading || svc.Reader.Active()) {
		svc.Reader.Stop() // keeps the position
		return "En pausa."
	}
	if svc.Player != nil && svc.Player.Active() {
		if err := svc.Player.Radio.Pause(); err != nil {
			return failed(err)
		}
		pausedPlayer = true
		return "En pausa."
	}
	return "No hay nada sonando para pausar."
}

func resumeAll() string {
	if pausedPlayer && svc.Player != nil && svc.Player.Active() {
		pausedPlayer = false
		if err := svc.Player.Radio.Resume(); err != nil {
			return failed(err)
		}
		return "Sigo."
	}
	if svc.Reader != nil {
		if _, _, ok := svc.Reader.Resume(); ok {
			return "Sigo."
		}
	}
	return "No hay nada en pausa."
}
