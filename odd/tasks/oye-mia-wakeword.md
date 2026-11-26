# Oye Mia wake word, trained on the owner's PC (T8)
Objective: train `oye_mia.onnx` (openWakeWord classifier) locally on CPU, validate it, install it on the Echo as the second wake word. Branch: main. Delivery: ask-on-risk. Runner: Python scripts in wakeword/ (venv wakeword/.venv), checks by measured validation numbers.

## Specs
- S1 Regenerate the model on this PC: owner chose "En esta PC (Recomendado)" after the Colab session (Torque, muse.ai) was recycled twice and the file was lost.
- S2 Requirements from wakeword/prompt-colab-oye-mia.md: phrase "Oye Mia" (pronounced "oye mía"); Spanish Piper voices; positives "oye mía", "oye, mía", "oye mia", "¡oye mía!", "oye mía?"; adversarial negatives "oye mira", "hoy mía", "oye tía", "oye María", "oye mamá", "mía", "oye", "mira", "o sea mía", "voy a ir", "lo mío", "joya mía"; classifier I/O `[1, 16, 96]` -> `[1, 1]`, opset 13; standard melspectrogram + embedding extractor unchanged; "priorizando pocas activaciones falsas".
- S3 Validation to show: hit rate on unseen "oye mía" clips, false activations per hour on normal audio, recommended threshold, ONNX shapes confirmed.
- S4 Downloads approved by the owner ("dale", 2026-11-26): first ~4 GB of openwakeword_features_ACAV100M_2000_hrs_16bit.npy, validation_set_features.npy (0.18 GB), Python libs (~1 GB, isolated venv), Piper 1.2.0 + 8 Spanish voices (~0.6 GB).
- S5 Install on the Echo per device notes: `/data/libreecho/wakeword-models/<name>/` with melspectrogram.onnx, embedding_model.onnx and the classifier; `config/wake-second` adds a second word.

## Tasks
- T1 S4 venv + downloads (background) — inline — DONE 2026-11-26 (acav_part 4.0 GB = 1.30M of 5.63M windows; validation 481345 frames = 10.7 h; 8 voices; piper 1.2.0; torch CPU + openwakeword 0.6.0 + scikit-learn)
- T2 S2 port the Colab v4 pipeline to wakeword/train.py: clips (parallel Piper), augmentation, features, real ACAV negatives, training, export — inline — running (background, nice 10)
- T3 S3 validate with openwakeword.Model streaming on held-out clips + validation_set_features FP/hour — inline
- T4 S5 install on the Echo, live test (owner says "oye mía"), rollback documented — inline — Risk item 5 (device config)

## Log
- L1 2026-11-26 Owner: "si probemos" (test Torque's model on the Echo); Colab had no active session, model lost; owner chose "En esta PC (Recomendado)"; approved downloads: "dale".
- L2 2026-11-26 Cause of Torque's high false activations (~100/h): its notebook trains only on synthetic Piper negatives; the official recipe adds ~2000 h of real negative features (ACAV100M). Only ~4 GB fit on disk (18 GB free), fetched with an HTTP Range request.
