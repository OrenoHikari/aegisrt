#!/usr/bin/env python3

import argparse
import json
import os
import subprocess
import sys
import time


def emit(event: str, **fields: object) -> None:
    payload = {
        "source": "descendant-agent",
        "event": event,
        "pid": os.getpid(),
        **fields,
    }
    print(json.dumps(payload), flush=True)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--seconds", type=int, default=30)
    args = parser.parse_args()

    child_code = """
import json
import os
import time

print(
    json.dumps({
        "source": "descendant-child",
        "event": "started",
        "pid": os.getpid()
    }),
    flush=True,
)

time.sleep(300)
"""

    child = subprocess.Popen(
        [
            sys.executable,
            "-c",
            child_code,
            "capsulert-descendant-marker",
        ]
    )

    emit(
        "started",
        child_pid=child.pid,
        message="parent and child are running",
    )

    for step in range(1, args.seconds + 1):
        emit(
            "heartbeat",
            step=step,
            total=args.seconds,
            child_pid=child.pid,
            child_alive=child.poll() is None,
        )
        time.sleep(1)

    emit(
        "unexpected_completion",
        child_pid=child.pid,
        message="Runtime timeout did not occur",
    )


if __name__ == "__main__":
    main()
