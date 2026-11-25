---
name: libreecho-vigia
description: "Trigger: falla de Mia, alerta del vigilante, vigia, ntfy alert, error en el Echo, chequeo de salud. Diagnose Echo failures, reuse known fixes, search GitHub, fix and learn."
license: MIT
metadata:
  author: "lopez848"
  version: "1.0"
---

## Activation Contract

Use when the owner reports a Mia/Echo failure, forwards a vigia ntfy alert, or asks for a health check of the LibreEcho biscuit.

## Hard Rules

- Load `libreecho-biscuit` first and obey its rules: config only, never reflash, back up before editing, ADB serial `G090LF1173650V3V`.
- Reproduce before fixing; a fix counts only after `vigia -once` prints `ok` and the original symptom is gone.
- GitHub is read-only: search and read issues/code; never open issues, comments or PRs without the owner's explicit request. Check the license before copying code.
- Write device paths in full in shell commands (no `rm` through variables); never print or commit the ntfy topic (`/data/libreecho/local/vigia.conf`).
- Every new fix is appended to `references/known-fixes.md` in the same session.

## Decision Gates

| Situation | Action |
|-----------|--------|
| Key or text matches `references/known-fixes.md` | Apply that fix, then verify |
| No match | Search GitHub (`references/sources.md`), then fix with evidence |
| `recovered` with no later failure | Record only; investigate if the same key repeats within a day |
| Needs a product decision or risky change | Stop and ask the owner one question |

## Execution Steps

1. Read history: `adb -s G090LF1173650V3V shell cat /data/libreecho/local/vigia/events.jsonl` (older: `events.jsonl.1`).
2. Current state: `adb -s G090LF1173650V3V shell 'cd /data/libreecho/local/services/vigia && ./vigia -once'`.
3. Group events by `key`; open problems are `alerted` without a later `recovered`, plus repeated instant keys (`log:*`, `restart:*`).
4. Read the matching service log in `/var/log/libreecho/local/` and reproduce.
5. Match known fixes; otherwise search GitHub with the error text minus numbers, ids and paths.
6. Fix, verify (step 2 + symptom), and add a test in `puente/` or `vigia/` when code changed.
7. Append the known fix, commit to `~/Proyectos/libreecho` with a Conventional Commit, and save it to Engram project `libreecho`.

## Output Contract

Report per problem: cause, fix, verification output, source (known fix or GitHub URL), rollback, and the known-fixes entry added.

## References

- `references/known-fixes.md` — learned failures: signature, cause, fix, verification.
- `references/sources.md` — components, their GitHub repos and search commands.
