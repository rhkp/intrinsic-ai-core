# This project was developed with assistance from AI tools.
"""Offline validation of current publishable files; never query a cluster or Git history."""

import argparse
import importlib.metadata
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

from setup_validation import ROOT, gitleaks_binary

GO_MODULE = ROOT / "openshift/deployment/chartassignment-controller"


class ValidationError(Exception):
    pass


def run(command, *, cwd=ROOT, environment=None):
    result = subprocess.run(
        [str(item) for item in command],
        cwd=cwd,
        env=environment,
        capture_output=True,
        text=True,
        timeout=600,
    )
    if result.returncode:
        # Linter/test output may include the offending source. Do not echo it.
        print(
            f"Command failed: {Path(str(command[0])).name} (exit {result.returncode})",
            file=sys.stderr,
        )
        for line in (result.stdout + result.stderr).splitlines():
            if re.match(r"^(FAIL: |ERROR: |Ran [0-9]+ tests? |FAILED \(|OK\b)", line):
                print(line, file=sys.stderr)
        raise ValidationError("command_failed: " + str(command[0]))
    for line in result.stderr.splitlines():
        if re.match(r"^(Ran [0-9]+ tests? |OK\b)", line):
            print(line)
    return result.stdout


def repository_files(root=ROOT):
    output = run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=root,
    )
    result = []
    for name in sorted(set(filter(None, output.split("\0")))):
        relative = Path(name)
        if relative.is_absolute() or ".." in relative.parts:
            raise ValidationError("invalid_repository_path")
        path = root / relative
        if any(
            (root / Path(*relative.parts[:count])).is_symlink()
            for count in range(1, len(relative.parts) + 1)
        ):
            raise ValidationError("symlinks_not_allowed_in_validation_input")
        if not path.is_file():
            continue
        if (
            (relative.name == ".env" or relative.name.startswith(".env."))
            and relative.name != ".env.sample"
            or relative.name.endswith(".env")
            or relative.name
            in {
                "id_rsa",
                "id_ed25519",
                "id_ecdsa",
                "id_dsa",
                ".netrc",
                ".npmrc",
                ".pypirc",
                "kubeconfig",
                "credentials.json",
            }
            or relative.suffix
            in {
                ".pem",
                ".key",
                ".crt",
                ".cer",
                ".der",
                ".csr",
                ".p8",
                ".p7b",
                ".p7c",
                ".p12",
                ".pfx",
                ".jks",
                ".keystore",
                ".kubeconfig",
                ".token",
                ".secret",
                ".credentials",
            }
            or any(
                part
                in {
                    ".git",
                    ".tools",
                    ".venv",
                    ".generated",
                    ".aws",
                    ".ssh",
                    ".kube",
                    ".docker",
                    ".secrets",
                    "secrets",
                    ".codegraph",
                }
                for part in relative.parts
            )
        ):
            raise ValidationError("private_file_selected: " + name)
        result.append(relative)
    return result


def owned_files(files, root=ROOT):
    manifest = json.loads(
        (root / "openshift/deployment/SOURCE_MANIFEST.json").read_text()
    )
    upstream = {
        Path("openshift/deployment") / entry["destination_path"]
        for entry in manifest["files"]
    }
    return [path for path in files if path not in upstream]


def check_tools():
    if sys.version_info < (3, 12):
        raise ValidationError("python_3_12_or_newer_required")
    for line in (ROOT / "requirements-validation.txt").read_text().splitlines():
        if not line or line.startswith("#"):
            continue
        package, version = line.split("==")
        if importlib.metadata.version(package) != version:
            raise ValidationError("validation_dependency_version_mismatch: " + package)
    gitleaks_binary()
    if not go_executable().is_file():
        raise ValidationError("cached_go_toolchain_missing; run make setup-validation")


def scan_secrets(files, root=ROOT):
    executable = gitleaks_binary()
    # No .git directory, ignored evidence, private configuration or history is copied.
    with tempfile.TemporaryDirectory(prefix="intrinsic-validation-") as directory:
        private = Path(directory)
        snapshot = private / "files"
        snapshot.mkdir(mode=0o700)
        for relative in files:
            destination = snapshot / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes((root / relative).read_bytes())
            destination.chmod(0o600)
        report = private / "findings.json"
        ignore = private / "empty-ignore"
        ignore.touch(mode=0o600)
        command = [
            str(executable),
            "dir",
            str(snapshot),
            "--config",
            str(ROOT / ".gitleaks.toml"),
            "--redact=100",
            "--ignore-gitleaks-allow",
            "--gitleaks-ignore-path",
            str(ignore),
            "--no-banner",
            "--log-level",
            "error",
            "--timeout",
            "60",
            "--report-format",
            "json",
            "--report-path",
            str(report),
        ]
        result = subprocess.run(
            command, cwd=private, capture_output=True, text=True, timeout=90
        )
        if not report.exists():
            raise ValidationError("secret_scan_did_not_produce_a_report")
        findings = json.loads(report.read_text())
        if not isinstance(findings, list):
            raise ValidationError("invalid_secret_scan_report")
        if result.returncode or findings:
            for finding in findings:
                name = (
                    Path(finding["File"]).resolve().relative_to(snapshot.resolve())
                    if Path(finding["File"]).is_absolute()
                    else Path(finding["File"])
                )
                print(
                    json.dumps(
                        {
                            "file": str(name),
                            "line": finding["StartLine"],
                            "rule": finding["RuleID"],
                        }
                    ),
                    file=sys.stderr,
                )
            raise ValidationError(
                "secret_scan_failed; matched values and raw scanner output suppressed"
            )


def lint_python(files, root=ROOT):
    paths = [str(path) for path in owned_files(files, root) if path.suffix == ".py"]
    if not paths:
        return
    result = subprocess.run(
        [
            sys.executable,
            "-m",
            "ruff",
            "check",
            "--no-cache",
            "--config",
            ROOT / "ruff.toml",
            "--output-format",
            "json",
            *paths,
        ],
        cwd=root,
        capture_output=True,
        text=True,
        timeout=60,
    )
    if result.returncode:
        for error in json.loads(result.stdout or "[]"):
            print(
                json.dumps(
                    {
                        "file": str(Path(error["filename"]).relative_to(root)),
                        "line": error["location"]["row"],
                        "rule": error["code"],
                    }
                ),
                file=sys.stderr,
            )
        raise ValidationError("python_lint_failed")


def lint_yaml(files, root=ROOT):
    from yamllint import linter
    from yamllint.config import YamlLintConfig

    config = YamlLintConfig(file=str(ROOT / ".yamllint.yaml"))
    failed = False
    for path in owned_files(files, root):
        if path.suffix not in {".yaml", ".yml"}:
            continue
        for problem in linter.run(
            (root / path).read_text(), config, filepath=str(path)
        ):
            failed = True
            print(
                json.dumps(
                    {
                        "file": str(path),
                        "line": problem.line,
                        "column": problem.column,
                        "rule": problem.rule or "syntax",
                    }
                ),
                file=sys.stderr,
            )
    if failed:
        raise ValidationError("yaml_lint_failed")


def lint_shell(files, root=ROOT):
    for path in owned_files(files, root):
        if path.suffix == ".sh":
            try:
                run(["bash", "-n", path], cwd=root)
            except ValidationError:
                print(
                    json.dumps({"file": str(path), "rule": "shell-syntax"}),
                    file=sys.stderr,
                )
                raise


def lint_go(files, root=ROOT, module=GO_MODULE):
    paths = [
        str(root / path)
        for path in files
        if path.suffix == ".go" and (root / path).is_relative_to(module)
    ]
    goroot = go_executable().parents[1]
    unformatted = run([goroot / "bin/gofmt", "-l", *paths]).splitlines()
    if unformatted:
        for path in unformatted:
            print(
                json.dumps(
                    {"file": str(Path(path).relative_to(root)), "rule": "gofmt"}
                ),
                file=sys.stderr,
            )
        raise ValidationError("gofmt_reported_unformatted_files")
    run([go_executable(), "vet", "./..."], cwd=module, environment=go_environment())


def go_executable():
    return Path((ROOT / ".tools/go-root.txt").read_text().strip()) / "bin/go"


def go_environment():
    return {**os.environ, "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local"}


def test_python():
    for directory in ("openshift", "tests"):
        output = run(
            [
                sys.executable,
                "-m",
                "unittest",
                "discover",
                "-s",
                directory,
                "-p",
                "test_*.py",
            ]
        )
        if output:
            print(output, end="")


def checks(files):
    return {
        "scan-secrets": lambda: scan_secrets(files),
        "lint-python": lambda: lint_python(files),
        "lint-yaml": lambda: lint_yaml(files),
        "lint-shell": lambda: lint_shell(files),
        "lint-go": lambda: lint_go(files),
        "test-python": test_python,
        "test-go": lambda: run(
            [go_executable(), "test", "./..."],
            cwd=GO_MODULE,
            environment=go_environment(),
        ),
        "validate-config": lambda: run(
            [sys.executable, "openshift/audit/audit_config.py", "--strict"]
        ),
        "validate-provenance": lambda: run(
            [sys.executable, "openshift/validate_source_manifest.py"]
        ),
        "check-whitespace": lambda: (
            run(["git", "diff", "--check"]),
            run(["git", "diff", "--cached", "--check"]),
        ),
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", choices=["validate", *checks([])])
    target = parser.parse_args(argv).target
    try:
        check_tools()
        files = repository_files()
        selected = checks(files)
        for name in selected if target == "validate" else [target]:
            print("CHECK " + name, flush=True)
            selected[name]()
            print("PASS  " + name, flush=True)
        print(
            f"Validation passed ({len(files)} current files; no cluster access or history scan)."
        )
        return 0
    except ValidationError as error:
        print("Validation failed: " + str(error), file=sys.stderr)
        return 1
    except (
        OSError,
        ValueError,
        KeyError,
        importlib.metadata.PackageNotFoundError,
        subprocess.SubprocessError,
    ):
        print(
            "Validation failed. Run the focused make target to isolate the check; source and secrets are not echoed.",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    sys.exit(main())
