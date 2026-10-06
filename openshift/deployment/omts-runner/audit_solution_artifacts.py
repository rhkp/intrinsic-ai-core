#!/usr/bin/env python3
"""Check user-owned OMTS solution outputs without printing matched values.

Pass the generated solution descriptor, runfiles manifest, and sanitized
project-specific config. Pinned upstream image/model payloads are separate
third-party artifacts and are not treated as project-owned configuration.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


IPV4 = re.compile(
    rb"(?<![0-9.])(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])"
    rb"(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}(?![0-9.])"
)
PRIVATE_KEY = re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----")
CREDENTIAL_ASSIGNMENT = re.compile(
    rb"(?i)(?:password|passwd|client[_-]?secret|api[_-]?key|access[_-]?token)"
    rb"\s*[:=]\s*[\"']?[A-Za-z0-9_./+=-]{12,}"
)
EXCLUDED_ROBOT_CONFIG = b"ur_module_config.textproto"
CHUNK_SIZE = 1024 * 1024
OVERLAP = 4096


def scan(path: Path) -> tuple[int, dict[str, int]]:
    counts = {
        "excluded_robot_config_refs": 0,
        "ipv4_literals": 0,
        "private_key_markers": 0,
        "credential_assignments": 0,
    }
    size = 0
    tail = b""
    with path.open("rb") as stream:
        while chunk := stream.read(CHUNK_SIZE):
            size += len(chunk)
            data = tail + chunk
            boundary = len(tail)
            patterns = {
                "excluded_robot_config_refs": re.finditer(re.escape(EXCLUDED_ROBOT_CONFIG), data),
                "ipv4_literals": IPV4.finditer(data),
                "private_key_markers": PRIVATE_KEY.finditer(data),
                "credential_assignments": CREDENTIAL_ASSIGNMENT.finditer(data),
            }
            for key, matches in patterns.items():
                counts[key] += sum(match.end() > boundary for match in matches)
            tail = data[-OVERLAP:]
    return size, counts


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifacts", nargs="+", type=Path)
    args = parser.parse_args()

    failed = False
    for path in args.artifacts:
        if not path.is_file():
            print(f"missing artifact: {path}", file=sys.stderr)
            failed = True
            continue
        size, counts = scan(path)
        print(f"{path.name}: bytes={size}, " + ", ".join(f"{key}={value}" for key, value in counts.items()))
        failed |= any(counts.values())
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
