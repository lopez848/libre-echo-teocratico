# wol.jw.org readings by voice
Objective: Mia reads this week's (or next week's) Watchtower study article, the meeting workbook (Guía de actividades), the weekly Bible reading, and any Bible chapter or verses from wol.jw.org, as a continuous reading that can be stopped and resumed. Branch: main. Delivery: ask-on-risk. Runner: `GOTOOLCHAIN=local ~/.local/share/go-echo/go1.27.4/bin/go test ./...` in puente/.

## Specs
- S1 Owner request (verbatim): "hay muchas cosas q de wol.jw.org q quiero incluir por ejemplo el estudio de la atalaya de esta semana y por ende tal ves la de la seguiente claro serian pediciones diferentes tambien algun texto de la biblia en especifico o un capitulo tambien la guia de estudio de esta semanana o la lectura de la biblia de esta semana cosas asi"
- S2 Watchtower study article of this week, and of next week as a separate request.
- S3 A specific Bible text (verse or range) or a whole chapter.
- S4 The meeting workbook ("guía de estudio" / Guía de actividades) of this week.
- S5 The weekly Bible reading (chapters listed in the workbook week heading, e.g. "LAMENTACIONES 3-5").
- S6 Reading mode chosen by the owner: "Seguido y retomable (Recomendado)" = reads everything continuously, paragraph by paragraph with each paragraph's question; "para" stops; "continúa la lectura" resumes from the paragraph where it stopped; "lee el párrafo 5" jumps to a paragraph.

## Tasks
- T1 S2-S5 wol.go: week pages (/es/wol/meetings/r4/lp-s/<ISO year>/<ISO week>), article, workbook and Bible chapter parsers into ordered reading pieces; tests on small synthetic HTML fixtures — inline — DONE 2026-11-26 (RED->GREEN 6 tests; live: this week article 22 pieces, next week article, workbook with weekly reading LAMENTACIONES 3-5, weekly chapters 30 pieces, Juan 3:16-17, Salmo 23)
- T2 S6 resumable reader: speaks pieces through the main ttsd after agentd's intro, remembers the position on stop, resumes, jumps to a paragraph — inline — DONE 2026-11-26 (RED->GREEN 4 tests, -race clean)
- T3 S2-S6 voice tools (read_watchtower, read_workbook, read_bible, read_weekly_bible_reading, resume_reading), Spanish phrasing, docs — inline — DONE 2026-11-26 (RED->GREEN 5 tests, -race clean)
- T4 deploy to the Echo, live test each request, rollback note — inline — DONE 2026-11-26 (live: Atalaya this week, para, continúa desde el párrafo 1, párrafo 5, Juan 3:16 direct, guía de actividades, para; rollback: previous binary puente.bak-go121-20261125 or git revert)

## Log
- L1 2026-11-26 Owner request: see S1. Reading mode answer: "Seguido y retomable (Recomendado)".
- L2 2026-11-26 Explored wol.jw.org: week page 2026/48 links the workbook "23-29 de noviembre" (/es/wol/d/r4/lp-s/202026404) and the study article "El sacerdocio muestra el amor de Jehová" (/es/wol/d/r4/lp-s/2026565). Article: p.qu question then its paragraph (data-pid), h2 subheadings, "¿QUÉ RESPONDERÍAS?" list, songs in p.pubRefs. Workbook: h2 sections, h3 parts, p/li details; h2 right after the date is the weekly Bible reading. Bible: /es/wol/b/r4/lp-s/nwtsty/<book>/<chapter>, verse spans id v<book>-<ch>-<v>-<part>, strip a.cl/a.vl numbers, a.b "+", a.fn "*". Fixtures are synthetic (no copies of jw.org pages in the public repo).
- L3 2026-11-26 Live run found: scripture links in articles share class "b" with the Bible's "+" links (refs and the weekly heading were being deleted); footnotes can be class "fn pr"; articles write refs with full names ("Hebreos 5:1") and the workbook with dot-less abbreviations ("Lam 3:22", "Mt 6:34"), which SpeakBibleRefs does not read, so wol.go adds speakWrittenRefs instead of changing the daily-text helper. Fixed with regression cases.
- L4 2026-11-26 Live results on the Echo: "Voy a leer el estudio de La Atalaya de esta semana: El sacerdocio muestra el amor de Jehová." / "Sigo con El sacerdocio muestra el amor de Jehová, desde el párrafo 1." / "Leyendo el párrafo 5 de El sacerdocio muestra el amor de Jehová." / Juan 3:16 answered directly / "Voy a leer la guía de actividades de esta semana." One cloud TTS timeout fell back to the local voice. Fixed after the run: a passage no longer starts with the previous verse's closing quote. Open: workbook references with dot-less abbreviations not in wbAbbrev are read as written.
