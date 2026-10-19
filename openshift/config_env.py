#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Emit quoted canonical shell settings after validating the exact cluster."""

import argparse
from pathlib import Path

from common import PilotError
from render_config import DEFAULT_CONFIG_PATH, render_env
from runtime_config import load_runtime_target


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    args = parser.parse_args()
    try:
        target = load_runtime_target(args.config)
        target.connect()
    except PilotError:
        parser.exit(
            2, "Canonical target validation failed; cluster details suppressed.\n"
        )
    for line in render_env(target.document).splitlines():
        if line and not line.startswith("#"):
            print("export " + line)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
