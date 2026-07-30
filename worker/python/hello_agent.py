#!/usr/bin/env python3

import argparse
import json
import os
import time


def emit(event: str, **fields: object) -> None:
    payload = {
        "source": "agent-worker",
        "event": event,
        "pid": os.getpid(),
        **fields,
    }
    print(json.dumps(payload, ensure_ascii=False), flush=True)


def main() -> None:
    parser = argparse.ArgumentParser(description="CAPSuleRT hello Agent")
    parser.add_argument(
        "--seconds",
        type=int,
        default=3,
        help="Number of heartbeat iterations",
    )
    args = parser.parse_args()

    emit("started", message="hello agent started")

    for step in range(1, args.seconds + 1):
        emit(
            "heartbeat",
            step=step,
            total=args.seconds,
        )
        time.sleep(1)

    emit(
        "result",
        status="ok",
        output="hello from the first CAPSuleRT Agent",
    )


if __name__ == "__main__":
    main()
