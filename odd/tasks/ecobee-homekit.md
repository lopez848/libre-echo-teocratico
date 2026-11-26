# Ecobee thermostat control by voice (HomeKit, local)

Objective: Mia controls the owner's ecobee3 lite ("Aire acondicionado", 192.168.1.217, HAP port 53940, sf=1 unpaired) over HomeKit on the local network, without ecobee's cloud API (closed to new developer keys since 2024-03).
Branch: feat/ecobee. Delivery: ask-on-risk. Test runner: `go test -race ./...` in `puente/` with the patched toolchain.

## Specs

- S1. Owner request: "tengo un termostato q controla el aire acondicionado y la calefaccion del apt de la marca ecobee podremos integrarlo?" and "sigamos con el ecobee".
- S2. (Assumption, to confirm with the owner) Voice actions: read the current temperature and mode; set the target temperature; switch mode among off, heat, cool and auto.
- S3. (Constraint) Pairing keys give control of the thermostat: stored only on the Echo, mode 600, never in the repo, a release or logs. The 8-digit HomeKit setup code is given by the owner and never logged.
- S4. (Constraint) HomeKit allows one pairing controller unless the controller adds others: once Mia pairs, Apple Home cannot add the thermostat until Mia's pairing is removed.

## Tasks

- T1 (S3) HAP controller: pair-setup (SRP-6a 3072/SHA-512 + Ed25519), pair-verify (X25519, ChaCha20-Poly1305 sessions), encrypted HTTP; tested against a real accessory implementation (github.com/brutella/hap) in tests. Route: inline.
- T2 (S2) thermostat model: read/write characteristics (current temp 0x11, target temp 0x35, target state 0x33, current state 0x0F, units 0x36). Route: inline.
- T3 (S2, S3) voice tools in puente + pairing flow + help text. Route: inline.
- T4 (S1) pair with the real thermostat on the owner's Echo and test by voice. Route: inline.
- T5 review by Gentle Shell (security: pairing keys, crypto). Route: delegated.

## Log

- L1 (2026-11-26) Owner: "tengo un termostato q controla el aire acondicionado y la calefaccion del apt de la marca ecobee podremos integrarlo?"
- L2 (2026-11-26) Research: ecobee stopped issuing developer API keys on 2024-03-28 (community reports, no reopening found); HomeKit local control is the usual alternative.
- L3 (2026-11-26) Owner: "sigamos con el ecobee". mDNS: `Aire acondicionado` _hap._tcp 192.168.1.217:53940 md=ecobee3 lite ci=9 sf=1 (not paired). From the Echo, GET /accessories answers 470 (connection authorization required): reachable.
