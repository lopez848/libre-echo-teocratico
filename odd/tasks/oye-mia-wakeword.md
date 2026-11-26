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
- T2 S2 port the Colab v4 pipeline to wakeword/train.py: clips (parallel Piper), augmentation, features, real ACAV negatives, training, export — inline — DONE 2026-11-26 (best checkpoint step 15000)
- T3 S3 validate with openwakeword.Model streaming on held-out clips + validation_set_features FP/hour — inline — DONE 2026-11-26 (streaming recall 0.832/0.822/0.796/0.772/0.736 and adversarial FP 0.068/0.052/0.042/0.032/0.022 at th 0.5-0.9; real-audio FP/h 1.40 at 0.5, 0.19 at 0.7, 0.09 at 0.8)
- T4 S5 install on the Echo, live test (owner says "oye mía"), rollback documented — inline — Risk item 5 (device config) — installed 2026-11-26 (/data/libreecho/wakeword-models/oye_mia/alexa_v0.1.onnx, config/wake-second = oye_mia, waked restarted with --second-model-dir); live test by the owner pending. Rollback: rm /data/libreecho/config/wake-second && /etc/init.d/libreecho-waked.init restart

## Log
- L1 2026-11-26 Owner: "si probemos" (test Torque's model on the Echo); Colab had no active session, model lost; owner chose "En esta PC (Recomendado)"; approved downloads: "dale".
- L2 2026-11-26 Cause of Torque's high false activations (~100/h): its notebook trains only on synthetic Piper negatives; the official recipe adds ~2000 h of real negative features (ACAV100M). Only ~4 GB fit on disk (18 GB free), fetched with an HTTP Range request.
- L3 2026-11-26 Training finished in ~2 h on CPU. Exported oye_mia.onnx (859 KB, opset 13, [1,16,96]->[1,1]). waked has one global threshold (init script sets none, default assumed 0.5): expect ~83% detection and ~1.4 false activations per hour; raising it would also change "Alexa". Model kept local (wakeword/data, ignored): not committed because the training negatives come from the openWakeWord feature set, whose license should be checked before publishing.
