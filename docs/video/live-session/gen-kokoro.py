#!/usr/bin/env python3
# Kokoro TTS generation for the ocibnkctl demo narration.
# Reads ~/piper/scenes3/scene{1..12}.txt, writes 24k wavs to an out dir.
# A second ffmpeg pass (in the shell wrapper) resamples to 22050/mono/s16.
import sys, os, numpy as np, soundfile as sf
from kokoro import KPipeline

VOICE = sys.argv[1] if len(sys.argv) > 1 else 'af_heart'
SRC   = os.path.expanduser('~/piper/scenes3')
OUT   = os.path.expanduser(sys.argv[2]) if len(sys.argv) > 2 else os.path.expanduser('~/piper/vo-kokoro-24k')
os.makedirs(OUT, exist_ok=True)

pipe = KPipeline(lang_code='a')  # American English
for n in range(1, 13):
    txt = open(f'{SRC}/scene{n}.txt').read().strip()
    chunks = []
    for _, _, audio in pipe(txt, voice=VOICE, speed=1.0):
        chunks.append(audio if isinstance(audio, np.ndarray) else audio.numpy())
    # small gap between internal segments for breathing room
    gap = np.zeros(int(0.12 * 24000), dtype=np.float32)
    full = np.concatenate([c for pair in zip(chunks, [gap]*len(chunks)) for c in pair][:-1]) if chunks else np.zeros(1, np.float32)
    sf.write(f'{OUT}/scene{n}.wav', full, 24000)
    print(f'  scene{n}: {len(full)/24000:.2f}s')
print('voice:', VOICE, '->', OUT)
