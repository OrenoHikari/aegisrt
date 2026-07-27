#!/usr/bin/env python3

import argparse
import json
import multiprocessing
import os
import time


def burn_cpu(deadline: float) -> None:
    value = os.getpid()

    while time.monotonic() < deadline:
        value = (value * 1664525 + 1013904223) & 0xFFFFFFFF


def main() -> None:
    parser = argparse.ArgumentParser()

    parser.add_argument(
        "--workers",
        type=int,
        default=max(2, (os.cpu_count() or 1) * 2),
    )

    parser.add_argument(
        "--seconds",
        type=float,
        default=25,
    )

    args = parser.parse_args()

    if args.workers <= 0:
        raise SystemExit("workers must be greater than zero")

    deadline = time.monotonic() + args.seconds

    processes = [
        multiprocessing.Process(
            target=burn_cpu,
            args=(deadline,),
        )
        for _ in range(args.workers)
    ]

    print(
        json.dumps({
            "source": "cpu-pressure",
            "event": "started",
            "pid": os.getpid(),
            "workers": args.workers,
            "seconds": args.seconds,
        }),
        flush=True,
    )

    for process in processes:
        process.start()

    for process in processes:
        process.join()

    print(
        json.dumps({
            "source": "cpu-pressure",
            "event": "completed",
            "pid": os.getpid(),
        }),
        flush=True,
    )


if __name__ == "__main__":
    main()
