#!/usr/bin/env python3

import argparse
import json
import os
import tempfile
import time
from pathlib import Path


def emit(event: str, **fields: object) -> None:
    payload = {
        "source": "profile-agent",
        "event": event,
        "pid": os.getpid(),
        **fields,
    }
    print(json.dumps(payload), flush=True)


def cpu_workload(seconds: float) -> dict[str, object]:
    deadline = time.monotonic() + seconds
    iterations = 0
    value = 1

    while time.monotonic() < deadline:
        value = (value * 1103515245 + 12345) % 2147483647
        iterations += 1

    return {
        "iterations": iterations,
        "value": value,
    }


def memory_workload(seconds: float) -> dict[str, object]:
    # Keep enough headroom below the Agent's 128 MiB cgroup limit.
    target_bytes = 48 * 1024 * 1024
    chunk_bytes = 4 * 1024 * 1024

    blocks: list[bytearray] = []

    while sum(len(block) for block in blocks) < target_bytes:
        block = bytearray(chunk_bytes)

        # Touch every memory page so the allocation is committed.
        for offset in range(0, len(block), 4096):
            block[offset] = 1

        blocks.append(block)

    deadline = time.monotonic() + seconds
    touches = 0

    while time.monotonic() < deadline:
        for block in blocks:
            block[touches % len(block)] ^= 1

        touches += 4096
        time.sleep(0.02)

    return {
        "allocated_bytes": sum(len(block) for block in blocks),
        "touches": touches,
    }


def io_workload(seconds: float) -> dict[str, object]:
    block = b"A" * (1024 * 1024)
    bytes_written = 0
    fsync_count = 0
    deadline = time.monotonic() + seconds

    temporary = tempfile.NamedTemporaryFile(
        prefix="aegisrt-io-",
        suffix=".tmp",
        dir="/tmp",
        delete=False,
    )
    path = Path(temporary.name)

    try:
        with temporary:
            while time.monotonic() < deadline:
                temporary.write(block)
                bytes_written += len(block)

                if bytes_written % (8 * 1024 * 1024) == 0:
                    temporary.flush()
                    os.fsync(temporary.fileno())
                    fsync_count += 1

                    # Avoid continuously growing the temporary file.
                    temporary.seek(0)
                    temporary.truncate(0)

        return {
            "bytes_written": bytes_written,
            "fsync_count": fsync_count,
        }
    finally:
        path.unlink(missing_ok=True)


def main() -> None:
    parser = argparse.ArgumentParser()

    parser.add_argument(
        "--profile",
        choices=("cpu", "memory", "io"),
        required=True,
    )

    parser.add_argument(
        "--seconds",
        type=float,
        default=3,
    )

    args = parser.parse_args()

    emit(
        "started",
        profile=args.profile,
        seconds=args.seconds,
    )

    started_at = time.monotonic()

    if args.profile == "cpu":
        result = cpu_workload(args.seconds)
    elif args.profile == "memory":
        result = memory_workload(args.seconds)
    else:
        result = io_workload(args.seconds)

    emit(
        "result",
        profile=args.profile,
        elapsed_seconds=time.monotonic() - started_at,
        **result,
    )


if __name__ == "__main__":
    main()
