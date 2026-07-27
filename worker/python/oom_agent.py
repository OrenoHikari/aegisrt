#!/usr/bin/env python3

import argparse
import json
import os
import time


def emit(event: str, **fields: object) -> None:
    payload = {
        "source": "oom-agent",
        "event": event,
        "pid": os.getpid(),
        **fields,
    }
    print(json.dumps(payload), flush=True)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--seconds", type=int, default=30)
    args = parser.parse_args()

    emit(
        "started",
        message="OOM Agent started",
        duration_seconds=args.seconds,
    )

    blocks: list[bytearray] = []
    chunk_size = 8 * 1024 * 1024
    allocated = 0
    deadline = time.monotonic() + args.seconds

    while time.monotonic() < deadline:
        block = bytearray(chunk_size)

        # Touch every page so that memory is physically committed.
        for offset in range(0, len(block), 4096):
            block[offset] = 1

        blocks.append(block)
        allocated += len(block)

        emit(
            "memory_allocated",
            allocated_bytes=allocated,
            allocated_mib=allocated // (1024 * 1024),
        )

        time.sleep(0.1)

    emit(
        "unexpected_completion",
        allocated_bytes=allocated,
        message="memory limit was not reached",
    )


if __name__ == "__main__":
    main()
