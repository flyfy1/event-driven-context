#!/usr/bin/env python3
"""Minimal stdin/stdout adapter for the reviewed local Qwen3-ASR model."""

import contextlib
import json
import os
import sys


def main() -> None:
    request = json.load(sys.stdin)
    max_tokens = int(request["max_tokens"])
    ffmpeg_path = os.path.realpath(request["ffmpeg_path"])
    if not os.path.isfile(ffmpeg_path) or not os.access(ffmpeg_path, os.X_OK):
        raise ValueError("configured ffmpeg executable is unavailable")
    os.environ["PATH"] = os.path.dirname(ffmpeg_path) + os.pathsep + os.environ.get("PATH", "")
    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    from mlx_audio.stt import load

    with contextlib.redirect_stdout(sys.stderr):
        model = load(request["model_path"])
        output = model.generate(
            request["audio_path"],
            language=request.get("language") or None,
            max_tokens=max_tokens,
            chunk_duration=float(request["chunk_duration"]),
            system_prompt=request.get("system_prompt") or None,
        )
    json.dump(
        {
            "text": output.text,
            "generation_tokens": output.generation_tokens,
            "complete": output.generation_tokens < max_tokens,
        },
        sys.stdout,
        ensure_ascii=False,
    )


if __name__ == "__main__":
    main()
