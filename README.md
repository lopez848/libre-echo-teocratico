# libreecho-mia

Spanish voice assistant ("Mia") for an Amazon Echo Dot (2nd gen, codename `biscuit`) running
LibreEcho v0.14.6. Configuration and a local bridge only: no reflash,
no bootloader or partition changes.

## What it does

- **Actions in Spanish**: timers, alarms, reminders, volume, do not disturb, stop, time and weather.
- **Content from jw.org**: the daily text and the music sections, shuffled, with "what's playing" and "next".
- **YouTube music**: songs by one artist ("pon canciones de Juan Luis Guerra"), one song followed by similar songs ("pon Provenza de Karol G"), or songs like the current one ("pon algo parecido a esto"). Audio is AAC, decoded on the Echo; no account needed. It learns from you: songs and artists you keep skipping stop showing up, and artists you listen to all the way come first. It relies on YouTube's app API, so it can break when YouTube changes it.
- **Fast cloud voice**: NVIDIA Magpie `ES-US.Mia`, served to the device over the Wyoming protocol,
  with the local Piper voice as a fallback.

## How it works

```
mic → STT (Spanish) → agentd ──OpenAI API──▶ puente (127.0.0.1:18080) ──▶ NVIDIA LLM
                                                 │  tool calls
                                                 ├──▶ LibreEcho web API (timers, alarms, volume…)
                                                 ├──▶ jw.org (daily text, music)
                                                 ├──▶ YouTube / YouTube Music (search, mixes, AAC audio)
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
| `vigia/` | Failure watcher: checks services, the bridge and its log every 30 s, records events and alerts the phone via ntfy |
| `skills/libreecho-vigia/` | Claude Code skill to diagnose failures, reuse known fixes and search GitHub |
| `wakeword/` | Google Colab prompt to train the "Oye Mia" wake word with openWakeWord |
| `run-one-shot.sh` | Wrapper for the LibreEcho one-shot installer |

## Build and test

The Echo kernel lacks `eventfd`. `puente` builds with the patched Go 1.27 toolchain (see
"Go toolchain for the Echo" below); `vigia` still builds with stock Go 1.21:

```sh
tools/go-echo/setup.sh            # once
G=~/.local/share/go-echo/go1.27.4/bin/go
cd puente
GOTOOLCHAIN=local $G test ./...
GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $G build -buildvcs=false -o puente-armv7 .
cd ../vigia
GOTOOLCHAIN=go1.21.13 CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -buildvcs=false -o vigia-armv7 .
```

## Run on the device

```sh
puente -listen 127.0.0.1:18080 -wyoming 127.0.0.1:10200
```

Main flags: `-upstream` (OpenAI-compatible URL), `-device` (LibreEcho web API), `-creds`
(file with the bridge's device user and password, two lines), `-key-file` (JSON with the NVIDIA
`api_key`), `-tts-voice`, `-local-tts`, `-tz`, `-radio`, `-v`. Run `puente -h` for all of them.

Secrets stay on the device under `/data/libreecho/`. Nothing in this repository holds a key.

## Failure watcher (vigia)

`vigia` runs on the Echo as a local service. Every 30 s it checks the local services, supervisor
restarts, the main daemons, the bridge `/health`, the cloud voice port, free space on `/data`
and new error lines in the bridge log. A lasting failure is rechecked after 90 s before it
alerts; the same failure alerts at most once per hour, including a service that keeps flapping.
Events go to `/data/libreecho/local/vigia/events.jsonl`; alerts go to an ntfy topic set in
`/data/libreecho/local/vigia.conf` (`topic=...`, kept on the device). Bridge log errors only send
the kind of error to the phone; the full line, which may contain what was said, stays in the
event log. Alerts that fail to send (no network) are queued (up to 20) and retried every check.

```sh
vigia -once                 # run the checks once, print problems, exit 1 if any
vigia -test-alert "texto"   # send a test notification
```

## Status

Pending work: YouTube playlists from the Echo, the "Oye Mia" wake word and a security review. See the feature log for details.

## License

MIT. See [LICENSE](LICENSE).

## Go toolchain for the Echo

The Echo kernel has no `eventfd`, which Go >= 1.22 needs. `tools/go-echo/setup.sh` makes a local
copy of a newer Go (default go1.27.4) with a one-function runtime patch that falls back to the
pipe Go 1.21 used. Build with `GOTOOLCHAIN=local ~/.local/share/go-echo/go1.27.4/bin/go build ...`.
