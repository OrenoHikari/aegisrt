#!/usr/bin/env python3

import argparse
import json
import os
from pathlib import Path


def required_path(name: str) -> Path:
    value = os.environ.get(name)

    if not value:
        raise RuntimeError(
            f"required environment variable {name} is missing"
        )

    return Path(value).resolve()


def emit(event: str, **fields: object) -> None:
    print(
        json.dumps(
            {
                "source": "integrity-dag-agent",
                "event": event,
                "pid": os.getpid(),
                **fields,
            }
        ),
        flush=True,
    )


def producer(label: str, staging: Path) -> None:
    results = staging / "results"
    results.mkdir(parents=True, exist_ok=True)

    payload = {
        "producer": label,
        "value": 42,
    }

    output_path = results / "payload.json"

    output_path.write_text(
        json.dumps(payload, indent=2) + "\n",
        encoding="utf-8",
    )

    emit(
        "produced",
        label=label,
        output=str(output_path),
    )


def consumer(label: str, staging: Path) -> None:
    raw = os.environ.get(
        "AEGIS_DEPENDENCY_OUTPUTS_JSON",
        "",
    )

    if not raw:
        raise RuntimeError(
            "dependency output metadata is missing"
        )

    dependencies = json.loads(raw)

    if not dependencies:
        raise RuntimeError("no dependencies were supplied")

    consumed = []

    for agent_id, output in dependencies.items():
        if not output.get("verified"):
            raise RuntimeError(
                f"dependency {agent_id} is not verified"
            )

        commit_path = Path(output["commit_path"])
        manifest_path = Path(output["manifest_path"])

        manifest = json.loads(
            manifest_path.read_text(encoding="utf-8")
        )

        files = manifest.get("files", [])

        if not files:
            raise RuntimeError(
                f"dependency {agent_id} has no artifacts"
            )

        first_artifact = (
            commit_path / files[0]["path"]
        )

        consumed.append(
            {
                "agent_id": agent_id,
                "verification_method":
                    output["verification_method"],
                "manifest_sha256":
                    output["manifest_sha256"],
                "artifact":
                    str(first_artifact),
                "content":
                    first_artifact.read_text(
                        encoding="utf-8"
                    ),
            }
        )

    results = staging / "results"
    results.mkdir(parents=True, exist_ok=True)

    output_path = results / "consumed.json"

    output_path.write_text(
        json.dumps(consumed, indent=2) + "\n",
        encoding="utf-8",
    )

    emit(
        "consumed",
        label=label,
        dependencies=len(consumed),
        output=str(output_path),
    )


def main() -> None:
    parser = argparse.ArgumentParser()

    parser.add_argument(
        "--role",
        choices=("producer", "consumer"),
        required=True,
    )

    parser.add_argument("--label", required=True)

    args = parser.parse_args()

    staging = required_path("AEGIS_OUTPUT_STAGING")

    if args.role == "producer":
        producer(args.label, staging)
    else:
        consumer(args.label, staging)


if __name__ == "__main__":
    main()
