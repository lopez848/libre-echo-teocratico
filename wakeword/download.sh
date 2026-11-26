#!/usr/bin/env sh
# Fetch training inputs into wakeword/data (owner-approved 2026-11-26).
set -eu
D="$(cd "$(dirname "$0")" && pwd)/data"; mkdir -p "$D/voices"
HF=https://huggingface.co/datasets/davidscripka/openwakeword_features/resolve/main
[ -s "$D/validation_set_features.npy" ] || curl -sSL -o "$D/validation_set_features.npy" "$HF/validation_set_features.npy"
# Only the first ~4 GB of the 17 GB negative set: the .npy rows are contiguous,
# so a byte range gives whole rows after the header (train.py trims the tail).
[ -s "$D/acav_part.npy" ] || curl -sSL -r 0-3999999999 -o "$D/acav_part.npy" "$HF/openwakeword_features_ACAV100M_2000_hrs_16bit.npy"
[ -x "$D/piper/piper" ] || { curl -sSL -o "$D/piper.tar.gz" https://github.com/rhasspy/piper/releases/download/v1.2.0/piper_amd64.tar.gz && tar -xzf "$D/piper.tar.gz" -C "$D" && rm "$D/piper.tar.gz"; }
V=https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0
for rel in es/es_MX/claude/high/es_MX-claude-high es/es_MX/ald/medium/es_MX-ald-medium es/es_AR/daniela/high/es_AR-daniela-high es/es_ES/davefx/medium/es_ES-davefx-medium es/es_ES/sharvard/medium/es_ES-sharvard-medium es/es_ES/carlfm/x_low/es_ES-carlfm-x_low es/es_ES/mls_9972/low/es_ES-mls_9972-low es/es_ES/mls_10246/low/es_ES-mls_10246-low; do
  n=$(basename $rel)
  for ext in .onnx .onnx.json; do [ -s "$D/voices/$n$ext" ] || curl -sSL -o "$D/voices/$n$ext" "$V/$rel$ext"; done
done
ls -la "$D" "$D/voices" | awk '{print $5, $9}'
echo DOWNLOADS_DONE
