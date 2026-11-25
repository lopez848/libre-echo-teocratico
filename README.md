# libreecho-mia

Spanish voice assistant ("Mia") for an Amazon Echo Dot (2nd gen, codename `biscuit`) running
LibreEcho v0.14.6. Configuration and a local bridge only: no reflash,
no bootloader or partition changes.

## What it does

- **Actions in Spanish**: timers, alarms, reminders, volume, do not disturb, stop, time and weather.
- **Content from jw.org**: the daily text and the music sections, shuffled, with "what's playing" and "next".
- **Fast cloud voice**: NVIDIA Magpie `ES-US.Mia`, served to the device over the Wyoming protocol,
  with the local Piper voice as a fallback.

## How it works

```
mic → STT (Spanish) → agentd ──OpenAI API──▶ puente (127.0.0.1:18080) ──▶ NVIDIA LLM
                                                 │  tool calls
                                                 ├──▶ LibreEcho web API (timers, alarms, volume…)
                                                 ├──▶ jw.org (daily text, music)
                                                 └──▶ radiod / audio bus (music playback)
ttsd ──Wyoming──▶ puente ──▶ NVIDIA Magpie TTS (fallback: local Piper)
```

`puente` (Spanish for "bridge") is a small Go service on the Echo. agentd uses it as its
OpenAI-compatible `base_url`. The bridge adds tool calling, runs the device actions with its own
device user, and streams the reply back as SSE.

## Layout

| Path | Contents |
|------|----------|
| `puente/` | Bridge source and tests |
| `odd/tasks/puente-acciones.md` | Feature log: specs, tasks, decisions and evidence |
| `wakeword/` | Google Colab prompt to train the "Oye Mia" wake word with openWakeWord |
| `run-one-shot.sh` | Wrapper for the LibreEcho one-shot installer |

## Build and test

The Echo kernel lacks `eventfd`, so build with Go 1.21:

```sh
cd puente
GOTOOLCHAIN=go1.21.13 go test ./...
GOTOOLCHAIN=go1.21.13 GOOS=linux GOARCH=arm GOARM=7 go build -o puente-armv7 .
```

## Run on the device

```sh
puente -listen 127.0.0.1:18080 -wyoming 127.0.0.1:10200
```

Main flags: `-upstream` (OpenAI-compatible URL), `-device` (LibreEcho web API), `-creds`
(file with the bridge's device user and password, two lines), `-key-file` (JSON with the NVIDIA
`api_key`), `-tts-voice`, `-local-tts`, `-tz`, `-radio`, `-v`. Run `puente -h` for all of them.

Secrets stay on the device under `/data/libreecho/`. Nothing in this repository holds a key.

## Status

Pending work: YouTube playlists from the Echo, a self-healing check for failures, the "Oye Mia"
wake word, a reboot test and a security review. See the feature log for details.
