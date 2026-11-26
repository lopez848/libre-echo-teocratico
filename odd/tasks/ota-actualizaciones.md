# OTA updates for gifted LibreEcho speakers

Objective: each speaker pulls signed releases of the bridge from GitHub Releases of lopez848/libre-echo-teocratico and installs them with automatic rollback (design: docs/actualizaciones-remotas.md, phase 1).
Branch: feat/ota. Delivery: ask-on-risk. Test runner: `go test -race ./...` in `ota/` with the patched toolchain (`GOTOOLCHAIN=local`, `~/.local/share/go-echo/go1.27.4/bin`).

## Specs

- S1. Updates reach every gifted speaker without visiting each home: "abria la posibilidad de poder subir el arreglo a distancia en cada hogas o por lo mejor subir el arreglo a algun lugar y q el aparato lo tome solo?"
- S2. The place to publish is GitHub, in the existing public repo: "creemos el repo en github ppara lo q te dije de las actualizaciones desde alli a los q regalare"; owner chose "Usar libreecho-mia (Recomendado)".
- S3. (From the approved design, docs/actualizaciones-remotas.md) Only releases signed with the owner's Ed25519 key, whose private half never leaves the owner's PC, are installed; every file is checked against the SHA-256 in the signed manifest; a version not newer than the installed one is ignored.
- S4. (Design) Install writes only under /data/libreecho/local/, keeps the previous binary as a backup, swaps atomically, restarts through the existing supervisor, checks health, and rolls back by itself when the new version is unhealthy; a failed version is not retried.
- S5. (Design) Per-home settings (credentials, ntfy topic, time zone, NVIDIA key, taste data) are never part of a release.
- S6. Publishing a release on GitHub is an outward action: each publish needs the owner's explicit yes in the conversation.

## Tasks

- T1 (S3, S5) ota-sign tool: keygen (private key 600 outside the repo) and manifest signing. Route: inline. Done (TestKeygenManifestAndSignRoundTrip).
- T2 (S1, S3, S4) ota agent: check latest release, verify signature, hash and version, install with backup, health check, rollback, state file. Route: inline. Done (7 agent tests RED then GREEN, -race; ARM build ok). Risk: items 2 and 5 (signature check, remote install) -> T5 review.
- T3 (S2, S6) release script: build armv7, write and sign manifest, `gh release create` only after the owner's yes. Route: inline.
- T4 (S1, S4) install the ota service on the owner's Echo and run one real update end to end (needs S6 yes). Route: inline.
- T5 review by Gentle Shell (DeepSeek) of the full diff (security + delivery risk). Route: delegated (global review rule).

## Log

- L1 (2026-11-26) Owner: "una pregunta estoy pensando en seguir buscando aparatos de estos en las tiendas de segunda poder configurarlos igual a este y regalarlo a amigos q tambien son testigos de jehova y puedan usar estas opciones pero como tenemos el problema de q youtube podria dejar de funcionar y cada quien lo tendria en su casa abria la posibilidad de poder subir el arreglo a distancia en cada hogas o por lo mejor subir el arreglo a algun lugar y q el aparato lo tome solo? dale la tarea a gentle-shell"
- L2 (2026-11-26) Owner: "me gustaria implementarlo pero antes quiero saber porq se detubo la reproduccion de la atalaya ..." (design accepted, implementation deferred).
- L3 (2026-11-26) Owner: "otra cosa antes de todo creemos el repo en github ppara lo q te dije de las actualizaciones desde alli a los q regalare". The public repo lopez848/libreecho-mia already exists; owner chose to use it.
- L4 (2026-11-26) Owner: "llamemoslo Libre Echo Teocratico al repo". Renamed on GitHub to lopez848/libre-echo-teocratico (no spaces allowed; GitHub redirects the old name); origin remote, README title and references updated.
- L5 (2026-11-26) T1+T2 written in ota/ (stdlib only). Public key is injected at build time (-X main.publicKey); without it the agent refuses to run. Only puente and vigia are updatable. Downgrades and failed versions are skipped.
