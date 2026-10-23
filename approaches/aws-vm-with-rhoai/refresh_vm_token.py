# This project was developed with assistance from AI tools.
"""Refresh only the model-scoped token; keep cluster credentials off the VM."""

import argparse
import json
from pathlib import Path
import shlex
import subprocess

from hybrid_config import ConfigError, load


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
    parser.add_argument("--env-file", type=Path)
    parser.add_argument("--context")
    parser.add_argument("--ssh-host")
    parser.add_argument("--ssh-key", type=Path)
    parser.add_argument("--namespace")
    parser.add_argument("--account")
    args = parser.parse_args()
    if args.env_file:
        if any(
            (args.context, args.ssh_host, args.ssh_key, args.namespace, args.account)
        ):
            parser.error(
                "Use --env-file or explicit target arguments, without mixing them"
            )
        try:
            config = load(args.env_file)
        except ConfigError as exc:
            parser.error(str(exc))
        args.context, args.namespace, args.account = (
            config.context,
            config.namespace,
            config.account,
        )
        args.ssh_host, args.ssh_key = config.ssh_host, config.ssh_key
    if not all((args.ssh_host, args.ssh_key, args.namespace, args.account)):
        parser.error("Select --env-file or supply all SSH/namespace/account arguments")
    if (
        args.ssh_host.startswith("-")
        or not args.ssh_host
        or shlex.split(args.ssh_host) != [args.ssh_host]
    ):
        raise ValueError("Use one SSH destination")
    refresh(
        args.ssh_host, args.ssh_key, args.namespace, args.account, context=args.context
    )
