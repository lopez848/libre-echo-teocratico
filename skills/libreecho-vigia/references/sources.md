# Components and where to search

| Component | Used for | GitHub |
|-----------|----------|--------|
| puente, vigia | Bridge and watcher (this project) | `lopez848/libreecho-mia` |
| LibreEcho OS | Device OS, agentd, sttd, ttsd, radiod, web API | `aslater3/LibreEcho` (also `aslater3/LibreEcho-Platform`, `aslater3/LibreEcho-UI`, `aslater3/LibreEcho-Linux-6.1`) |
| go-mp3 | MP3 decoding in BusPlayer | `hajimehoshi/go-mp3` (archived: read-only history) |
| sherpa-onnx | STT/TTS runtime used by sttd/ttsd models | `k2-fsa/sherpa-onnx` |
| Piper voices | Local Spanish voice (es_MX claude-high) | `rhasspy/piper` (archived: read-only history) |
| openWakeWord | Wake word models ("Oye Mia") | `dscripka/openWakeWord` |
| Wyoming protocol | Cloud voice bridge to ttsd | `OHF-Voice/wyoming` (moved from `rhasspy/wyoming`) |
| ntfy | Phone alerts | `binwiederhier/ntfy` |
| NVIDIA NIM / Riva | LLM and Magpie TTS | search `nvidia magpie tts` / `riva` |

Repositories verified with `gh repo view` on 2026-11-25.

## Search commands

```sh
gh search issues "<error text>" --repo <owner/repo> --limit 10
gh search issues "<error text>" --limit 10            # all of GitHub
gh search code "<error text>" --repo <owner/repo> --limit 10
gh issue view <n> --repo <owner/repo> --comments
```

Strip timestamps, numbers, ids and local paths from the error text before searching.
