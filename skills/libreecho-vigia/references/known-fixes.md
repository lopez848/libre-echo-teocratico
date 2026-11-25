# Known fixes

One entry per learned failure. Match on `key` (vigia event key) or the error text.
Append new entries at the end with the same fields.

## Bridge does not start after a Go upgrade
- Signature: `restart:puente` or `restart:vigia` in a loop right after deploying a new binary; crash at start.
- Cause: the Echo kernel lacks `eventfd`; Go 1.22+ binaries crash at start.
- Fix: rebuild with `GOTOOLCHAIN=go1.21.13 CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -buildvcs=false`.
- Verify: `service.<name>=running` in `/run/libreecho/local.status` after 30 s.
- Source: owner session 2026-11-24.

## Daily text reads the wrong date
- Signature: owner says "me dio otra fecha" for mañana/ayer.
- Cause: the model computed the date from the context line.
- Fix: `daily_text` takes `day` (hoy, mañana, ayer, pasado mañana, anteayer); the bridge adds the offset. Commit `c4eb42f`.
- Verify: `TestDailyTextToolRelativeDay`; live phrases return the expected weekday and date.
- Source: owner session 2026-11-25.

## Action ignored and the reply is a function name
- Signature: reply text like `cancelar_timers{}`; no device action.
- Cause: the model wrote the tool call as text with Spanish words.
- Fix: `resolveTool` in `puente/tools.go` translates Spanish words; add missing words to `toolWords`. Commit `9033960`.
- Verify: `TestTextToolCallForms`; live set/cancel round leaves `GET /timers` empty.
- Source: owner session 2026-11-25.

## Assistant goes silent on long replies
- Signature: agentd drops the answer; bridge log shows `turn done` but nothing is spoken.
- Cause: agentd drops one long SSE event.
- Fix: the bridge streams small deltas (already in `puente`); keep it that way when changing the streaming code.
- Verify: ask for the daily text and hear it complete.
- Source: owner session 2026-11-24.

## Music URL plays nothing
- Signature: `/var/log/libreecho-radiod.log` shows `no audio from … giving up`.
- Cause: radiod only decodes 48 kHz MP3; jw.org files are 44.1 kHz.
- Fix: play through the bridge BusPlayer (decodes, resamples to 48 kHz, writes `/run/libreecho-audio/media.pcm`).
- Verify: `now_playing` answers a title and audio is heard.
- Source: owner session 2026-11-24.

## Words with accents are spelled out
- Signature: the voice says "qu e acentuada" for "qué".
- Cause: the device TTS spells precomposed á é í ó ú.
- Fix: send NFD text (bridge `Speakable`), keep ñ precomposed.
- Verify: device TTS test in `libreecho-biscuit` references.
- Source: owner session 2026-11-24.

## Test script gets "Authentication is required"
- Signature: API calls from a test script fail with `forbidden` after the bridge acts.
- Cause: one session per user; the bridge and the script both log in as `puente` and invalidate each other.
- Fix: log in before every call in test scripts. Always DELETE leftover test timers.
- Verify: the script's `GET /timers` answers `ok:true`.
- Source: owner session 2026-11-25.
