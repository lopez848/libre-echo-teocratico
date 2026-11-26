"""Train the "Oye Mia" openWakeWord classifier on CPU.

Port of the Colab v4 notebook (oye_mia_colab_v4.ipynb) with two changes:
real negative features from openWakeWord's ACAV100M set are mixed into every
batch, and the best checkpoint is chosen by false activations per hour on
the ~11 h validation_set_features.npy, not on synthetic phrases only.
Every stage caches its output in data/, so a rerun resumes.

Usage: .venv/bin/python train.py [clips|features|train|validate|all]
"""
import json
import os
import random
import subprocess
import sys
import tempfile
from concurrent.futures import ProcessPoolExecutor
from pathlib import Path

import numpy as np
import soundfile as sf

BASE = Path(__file__).resolve().parent / "data"
PIPER = BASE / "piper" / "piper"
SR = 16000
SEED = 1234

TARGET_VARIANTS = ["oye mía", "oye, mía", "oye mia", "¡oye mía!", "oye mía?"]
ADVERSARIAL = ["oye mira", "hoy mía", "oye tía", "oye María", "oye mamá", "mía", "oye", "mira",
               "o sea mía", "voy a ir", "lo mío", "joya mía"]
GENERIC_NEG = ["buenos días", "cuánto cuesta", "pon la música", "qué hora es", "llama a mamá",
               "abre la puerta", "hace mucho calor hoy", "vamos a la tienda", "los niños ya comieron",
               "mañana salimos temprano", "apaga la luz", "sube el volumen"]
VOICES = ["es_MX-claude-high", "es_MX-ald-medium", "es_AR-daniela-high", "es_ES-davefx-medium",
          "es_ES-sharvard-medium", "es_ES-carlfm-x_low", "es_ES-mls_9972-low", "es_ES-mls_10246-low"]
SETS = {  # folder: (count, seed, texts)
    "clips_pos_train": (5000, SEED, "pos"),
    "clips_pos_test": (500, SEED + 999, "pos"),
    "clips_neg_train": (5000, SEED + 1, "neg"),
    "clips_neg_test": (500, SEED + 1000, "neg"),
}
STEPS = 50000
EVAL_EVERY = 1000
MAX_NEG_WEIGHT = 1000  # ramp of the negative class weight (official auto_train style)
TARGET_FP_PER_HOUR = 0.5
THRESHOLDS = [0.5, 0.6, 0.7, 0.8, 0.9]


# ---------------------------------------------------------------- clips

def synth(voice, text, rng, holdout):
    fd, tmp = tempfile.mkstemp(suffix=".wav")
    os.close(fd)
    cmd = [str(PIPER), "--model", str(BASE / "voices" / f"{voice}.onnx"), "--output_file", tmp,
           "--length-scale", str(rng.uniform(0.95, 1.15) if holdout else rng.uniform(0.8, 1.3)),
           "--noise-scale", str(rng.uniform(0.5, 0.8)), "--noise-w-scale", str(rng.uniform(0.6, 0.9))]
    if "sharvard" in voice:
        cmd += ["--speaker", str(rng.randrange(2))]
    r = subprocess.run(cmd, input=text.encode(), capture_output=True)
    if r.returncode != 0:
        raise RuntimeError(r.stderr.decode()[-300:])
    audio, src = sf.read(tmp)
    os.remove(tmp)
    audio = audio.astype(np.float32)
    if audio.ndim > 1:
        audio = audio[:, 0]
    if src != SR:
        audio = np.interp(np.linspace(0, 1, int(len(audio) * SR / src)), np.linspace(0, 1, len(audio)), audio).astype(np.float32)
    return audio


def to_2s(audio, rng):
    out = np.zeros(SR * 2, dtype=np.float32)
    start = rng.randrange(0, max(1, SR * 2 - len(audio))) if len(audio) < SR * 2 else 0
    seg = audio[: SR * 2 - start]
    out[start:start + len(seg)] = seg
    return out / max(np.max(np.abs(out)) + 1e-9, 1.0) * 0.89


def augment(x, rng):
    """Room echo, background noise and gain, as in the notebook (train only)."""
    if rng.random() < 0.7:
        t = int(rng.integers(800, 6000))
        rir = rng.standard_normal(t) * np.exp(-np.linspace(0, rng.uniform(3, 6), t))
        rir /= np.max(np.abs(rir)) + 1e-9
        y = np.convolve(x, rir)[: len(x)]
    else:
        y = x.copy()
    noise = np.cumsum(rng.standard_normal(len(y)))
    noise /= np.max(np.abs(noise)) + 1e-9
    noise += 0.05 * np.sin(2 * np.pi * 60 * np.arange(len(y)) / SR)
    snr = 10 ** (rng.uniform(5, 25) / 20)
    y = y + noise / max(1e-9, np.max(np.abs(y))) / snr * 0.5
    y *= 10 ** (rng.uniform(-12, 0) / 20)
    return np.clip(y, -1, 1).astype(np.float32)


def one_clip(args):
    folder, i, seed, kind = args
    path = BASE / folder / f"{i:05d}.wav"
    if path.exists():
        return
    rng = random.Random(seed * 100000 + i)
    if kind == "pos":
        text = rng.choice(TARGET_VARIANTS)
    else:
        text = rng.choice(ADVERSARIAL if rng.random() < 0.5 else GENERIC_NEG)
    voice = rng.choice(VOICES)
    x = to_2s(synth(voice, text, rng, holdout="test" in folder), rng)
    if "train" in folder:
        x = augment(x, np.random.default_rng(seed * 100000 + i))
    tmp = path.with_suffix(".tmp.wav")
    sf.write(tmp, x, SR)
    tmp.rename(path)


def stage_clips():
    jobs = []
    for folder, (count, seed, kind) in SETS.items():
        (BASE / folder).mkdir(parents=True, exist_ok=True)
        jobs += [(folder, i, seed, kind) for i in range(count)]
    with ProcessPoolExecutor(max_workers=os.cpu_count()) as ex:
        for n, _ in enumerate(ex.map(one_clip, jobs, chunksize=20)):
            if n % 1000 == 0:
                print(f"clips {n}/{len(jobs)}", flush=True)
    for folder in SETS:
        print(folder, len(list((BASE / folder).glob("[0-9]*.wav"))), flush=True)


# ---------------------------------------------------------------- features

def stage_features():
    out = BASE / "features.npz"
    if out.exists():
        print("features cached")
        return
    from openwakeword.utils import AudioFeatures
    af = AudioFeatures(inference_framework="onnx")

    def feats(folder):
        rows = []
        for p in sorted((BASE / folder).glob("[0-9]*.wav")):
            x, _ = sf.read(p)
            emb = np.array(af._get_embeddings((x * 32767).astype(np.int16)), dtype=np.float32)
            if len(emb) < 16:
                emb = np.pad(emb, ((16 - len(emb), 0), (0, 0)))
            rows.append(emb[-16:])
        return np.stack(rows)

    data = {k: feats(k) for k in SETS}
    np.savez(out, **data)
    print({k: v.shape for k, v in data.items()})


def load_acav():
    """Rows of the partially downloaded ACAV100M feature file (N, 16, 96)."""
    path = BASE / "acav_part.npy"
    with open(path, "rb") as f:
        major, _ = np.lib.format.read_magic(f)
        read = np.lib.format.read_array_header_1_0 if major == 1 else np.lib.format.read_array_header_2_0
        shape, _fortran, dtype = read(f)
        header = f.tell()
    row = int(np.prod(shape[1:])) * np.dtype(dtype).itemsize
    n = (path.stat().st_size - header) // row
    print(f"ACAV negatives: {n} of {shape[0]} windows ({n / shape[0]:.0%})", flush=True)
    return np.memmap(path, dtype=dtype, mode="r", offset=header, shape=(n,) + tuple(shape[1:]))


# ---------------------------------------------------------------- model

def make_net():
    import torch.nn as nn

    class FCNBlock(nn.Module):
        def __init__(self, d):
            super().__init__()
            self.fcn_layer, self.relu, self.layer_norm = nn.Linear(d, d), nn.ReLU(), nn.LayerNorm(d)

        def forward(self, x):
            return self.relu(self.layer_norm(self.fcn_layer(x)))

    class Net(nn.Module):
        # Same DNN as openwakeword.train.Model (layer_dim=128, 1 block).
        def __init__(self, layer_dim=128):
            super().__init__()
            self.flatten = nn.Flatten()
            self.layer1 = nn.Linear(16 * 96, layer_dim)
            self.relu1 = nn.ReLU()
            self.layernorm1 = nn.LayerNorm(layer_dim)
            self.blocks = nn.ModuleList([FCNBlock(layer_dim)])
            self.last_layer = nn.Linear(layer_dim, 1)
            self.last_act = nn.Sigmoid()

        def forward(self, x):
            x = self.relu1(self.layernorm1(self.layer1(self.flatten(x))))
            for b in self.blocks:
                x = b(x)
            return self.last_act(self.last_layer(x))

    return Net()


def fp_per_hour(scores, threshold, frame_s=0.08, refractory=1.0):
    """False activations per hour over consecutive windows (one per 80 ms)."""
    hits, last = 0, -1e9
    for i in np.flatnonzero(scores >= threshold):
        t = i * frame_s
        if t - last >= refractory:
            hits += 1
            last = t
    return hits / (len(scores) * frame_s / 3600)


def val_windows():
    v = np.load(BASE / "validation_set_features.npy", mmap_mode="r")
    if v.ndim == 2:  # continuous frames: slide a 16-frame window
        v = np.lib.stride_tricks.sliding_window_view(v, 16, axis=0).transpose(0, 2, 1)
    return v


def stage_train():
    import torch
    torch.manual_seed(SEED)
    torch.set_num_threads(os.cpu_count())
    d = np.load(BASE / "features.npz")
    pos, neg = torch.tensor(d["clips_pos_train"]), torch.tensor(d["clips_neg_train"])
    pos_te, neg_te = torch.tensor(d["clips_pos_test"]), torch.tensor(d["clips_neg_test"])
    acav = load_acav()
    val = val_windows()
    print("validation windows", val.shape, f"= {len(val) * 0.08 / 3600:.1f} h", flush=True)
    rng = np.random.default_rng(SEED)
    net = make_net()
    opt = torch.optim.Adam(net.parameters(), lr=1e-3)
    bce = torch.nn.BCELoss(reduction="none")
    best, log = None, []

    def scores(x, bs=8192):
        with torch.no_grad():
            return np.concatenate([net(torch.as_tensor(np.asarray(x[i:i + bs], dtype=np.float32))).numpy()[:, 0]
                                   for i in range(0, len(x), bs)])

    for step in range(1, STEPS + 1):
        net.train()
        ai = np.sort(rng.choice(len(acav), 512, replace=False))
        xb = torch.cat([pos[rng.integers(0, len(pos), 256)], neg[rng.integers(0, len(neg), 256)],
                        torch.as_tensor(np.asarray(acav[ai], dtype=np.float32))])
        yb = torch.cat([torch.ones(256), torch.zeros(768)])
        w = torch.ones(1024)
        w[256:] = 1 + (MAX_NEG_WEIGHT - 1) * min(1.0, step / (STEPS * 0.5))
        loss = (bce(net(xb)[:, 0], yb) * w).mean()
        opt.zero_grad()
        loss.backward()
        opt.step()
        if step % EVAL_EVERY:
            continue
        net.eval()
        sp, sn, sv = scores(pos_te), scores(neg_te), scores(val)
        for th in THRESHOLDS:
            rec, adv, fph = float((sp >= th).mean()), float((sn >= th).mean()), fp_per_hour(sv, th)
            row = {"step": step, "th": th, "recall": rec, "adv_fp": adv, "fp_h": fph}
            log.append(row)
            ok = fph <= TARGET_FP_PER_HOUR and adv <= 0.05
            if ok and (best is None or rec > best["recall"]):
                best = dict(row)
                torch.save(net.state_dict(), BASE / "best.pt")
        r = [x for x in log if x["step"] == step]
        print(f"step {step} loss {loss.item():.4f} | " + " ".join(
            f"th{x['th']}: rec {x['recall']:.3f} adv {x['adv_fp']:.3f} fp/h {x['fp_h']:.2f}" for x in r)
            + (f" | best {best}" if best else ""), flush=True)
    (BASE / "train_log.json").write_text(json.dumps({"best": best, "log": log}, indent=1))
    if best is None:
        print("no checkpoint met fp/h <= %.1f and adversarial FP <= 5%%" % TARGET_FP_PER_HOUR)
        sys.exit(1)
    export(best)


def export(best):
    import onnx
    import torch
    net = make_net()
    net.load_state_dict(torch.load(BASE / "best.pt"))
    net.eval()
    out = BASE / "oye_mia.onnx"
    torch.onnx.export(net, torch.rand(1, 16, 96), str(out), opset_version=13, input_names=["input"],
                      output_names=["output"], dynamic_axes=None, dynamo=False)
    m = onnx.load(str(out))
    onnx.checker.check_model(m)
    dims = lambda t: [x.dim_value for x in t.type.tensor_type.shape.dim]
    print("exported", out, "opset", m.opset_import[0].version, "in", dims(m.graph.input[0]),
          "out", dims(m.graph.output[0]), "best", best, flush=True)


# ---------------------------------------------------------------- validate

def stage_validate():
    """Score held-out clips through the normal openWakeWord streaming path."""
    from openwakeword.model import Model
    oww = Model(wakeword_models=[str(BASE / "oye_mia.onnx")], inference_framework="onnx")

    def score(p):
        oww.reset()
        x = (sf.read(p)[0] * 32767).astype(np.int16)
        x = np.concatenate([np.zeros(SR, np.int16), x, np.zeros(SR // 2, np.int16)])
        return max(list(oww.predict(x[i:i + 1280]).values())[0] for i in range(0, len(x), 1280))

    sp = np.array([score(p) for p in sorted((BASE / "clips_pos_test").glob("[0-9]*.wav"))])
    sn = np.array([score(p) for p in sorted((BASE / "clips_neg_test").glob("[0-9]*.wav"))])
    best = json.loads((BASE / "train_log.json").read_text())["best"]
    res = {"best_checkpoint": best, "streaming": []}
    for th in THRESHOLDS:
        res["streaming"].append({"th": th, "recall": float((sp >= th).mean()), "adv_fp": float((sn >= th).mean())})
    (BASE / "validation.json").write_text(json.dumps(res, indent=1))
    print(json.dumps(res, indent=1))


if __name__ == "__main__":
    stage = sys.argv[1] if len(sys.argv) > 1 else "all"
    for name, fn in [("clips", stage_clips), ("features", stage_features), ("train", stage_train),
                     ("validate", stage_validate)]:
        if stage in (name, "all"):
            print(f"== {name}", flush=True)
            fn()
