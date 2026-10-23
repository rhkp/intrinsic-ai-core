# This project was developed with assistance from AI tools.
"""Refresh only the model-scoped token; keep cluster credentials off the VM."""

import argparse
import json
from pathlib import Path
import subprocess

from hybrid_config import ConfigError, DEFAULT_CONFIG_PATH, load, verify_context


def refresh(host, key, namespace, account, *, context=None):
    result = subprocess.run(
        [
            "oc",
            *(["--context", context] if context else []),
            "create",
            "token",
            account,
            "-n",
            namespace,
            "--duration=1h",
            "--audience=https://kubernetes.default.svc",
        ],
        capture_output=True,
        text=True,
        timeout=30,
    )
    if result.returncode:
        raise RuntimeError("Model token issuance failed; details suppressed")
    token = result.stdout.strip()
    if not token or any(character.isspace() for character in token):
        raise RuntimeError("Invalid issued token")
    script = """import subprocess,json
token = TOKEN
p=subprocess.run(['kubectl','-n','app-resources','create','secret','generic','hybrid-rhoai-token','--from-file=token=/dev/stdin','--dry-run=client','-o','json'],input=token,text=True,capture_output=True,check=True)
r=subprocess.run(['kubectl','apply','-f','-'],input=p.stdout,text=True,capture_output=True,check=True)
print(json.dumps({'model_token_refreshed':True,'requested_lifetime_seconds':3600}))
""".replace("TOKEN", repr(token))
    result = subprocess.run(
        [
            "ssh",
            "-a",
            "-i",
            str(key.expanduser()),
            "-o",
            "IdentitiesOnly=yes",
            "-o",
            "BatchMode=yes",
            "-o",
            "StrictHostKeyChecking=yes",
            "--",
            host,
            "python3 -",
        ],
        input=script,
        capture_output=True,
        text=True,
        timeout=45,
    )
    if result.returncode:
        raise RuntimeError("VM token refresh failed; details suppressed")
    print(json.dumps(json.loads(result.stdout)))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    args = parser.parse_args()
    try:
        config = load(args.config)
        verify_context(config)
    except ConfigError as exc:
        parser.error(str(exc))
    refresh(
        config.ssh_host,
        config.ssh_key,
        config.namespace,
        config.account,
        context=config.context,
    )
