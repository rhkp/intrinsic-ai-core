#!/usr/bin/env python3
"""Fail closed if a packaged OMTS zip contains excluded hardware config."""

from __future__ import annotations

import argparse
import re
import sys
import zipfile
from pathlib import Path

EXCLUDED_SUFFIXES = (
    "configs/lab_bb_01/ur_module_config.textproto",
    "configs/omts/ur_module_config.textproto",
)
CONFIG_SUFFIXES = (".textproto", ".pbtxt", ".yaml", ".yml", ".json")
IPV4_LITERAL = re.compile(rb"(?<![0-9.])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![0-9.])")
PRIVATE_KEY_MARKERS = (b"-----BEGIN PRIVATE KEY-----", b"-----BEGIN RSA PRIVATE KEY-----")


def audit(archive_path: Path) -> list[str]:
    findings: list[str] = []
    with zipfile.ZipFile(archive_path) as archive:
        for member in archive.namelist():
            if member.endswith(EXCLUDED_SUFFIXES):
                findings.append(f"{archive_path.name}:{member}: excluded hardware config")
                continue
            if not member.endswith(CONFIG_SUFFIXES):
                continue
            content = archive.read(member)
            if IPV4_LITERAL.search(content):
                findings.append(f"{archive_path.name}:{member}: IPv4 literal in packaged config")
            if any(marker in content for marker in PRIVATE_KEY_MARKERS):
                findings.append(f"{archive_path.name}:{member}: private-key marker in packaged config")
    return findings


def audit_config_file(config_path: Path) -> list[str]:
    content = config_path.read_bytes()
    findings: list[str] = []
    if IPV4_LITERAL.search(content):
        findings.append(f"{config_path.name}: IPv4 literal in packaged config")
    if any(marker in content for marker in PRIVATE_KEY_MARKERS):
        findings.append(f"{config_path.name}: private-key marker in packaged config")
    return findings


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", action="append", type=Path, default=[])
    parser.add_argument("archives", nargs="+", type=Path)
    args = parser.parse_args()
    findings = [finding for path in args.archives for finding in audit(path)]
    findings.extend(finding for path in args.config for finding in audit_config_file(path))
    if findings:
        print("OMTS runner archive audit failed:", file=sys.stderr)
        for finding in findings:
            print(f"  {finding}", file=sys.stderr)
        return 1
    print(
        f"OMTS runner artifact audit passed for {len(args.archives)} archive(s) "
        f"and {len(args.config)} standalone config file(s)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
