# This project was developed with assistance from AI tools.
"""Install pinned validation tools; validation itself performs no downloads."""

import hashlib
import os
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import venv
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
VERSION = "8.30.1"
# Official release asset SHA-256 values, not downloaded executable code.
CHECKSUMS = {
    "darwin_arm64": "b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5",
    "darwin_x64": "dfe101a4db2255fc85120ac7f3d25e4342c3c20cf749f2c20a18081af1952709",
    "linux_arm64": "e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080",
    "linux_x64": "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb",
}


def tool_paths():
    architecture = {
        "aarch64": "arm64",
        "arm64": "arm64",
        "x86_64": "x64",
        "amd64": "x64",
    }
    key = (
        platform.system().lower()
        + "_"
        + architecture.get(platform.machine().lower(), "unknown")
    )
    if key not in CHECKSUMS:
        raise ValueError("validation_tools_require_linux_or_macos_arm64_or_x64")
    directory = ROOT / ".tools" / "gitleaks" / VERSION / key
    return key, directory / f"gitleaks_{VERSION}_{key}.tar.gz", directory / "gitleaks"


def verified_binary(archive, expected):
    if hashlib.sha256(archive.read_bytes()).hexdigest() != expected:
        raise ValueError("gitleaks_archive_checksum_mismatch")
    with tarfile.open(archive, "r:gz") as tar:
        member = tar.getmember("gitleaks")
        if not member.isfile() or not 0 < member.size <= 128 * 1024 * 1024:
            raise ValueError("invalid_gitleaks_archive_member")
        # Read only the expected binary; never extract paths or archive metadata.
        with tar.extractfile(member) as source:
            return source.read()


def gitleaks_binary():
    key, archive, executable = tool_paths()
    binary = verified_binary(archive, CHECKSUMS[key])
    if executable.is_symlink() or executable.read_bytes() != binary:
        raise ValueError("installed_gitleaks_differs_from_verified_archive")
    return executable


def install_gitleaks():
    key, archive, executable = tool_paths()
    archive.parent.mkdir(parents=True, exist_ok=True)
    if not archive.exists():
        url = f"https://github.com/gitleaks/gitleaks/releases/download/v{VERSION}/{archive.name}"
        with tempfile.TemporaryDirectory(dir=archive.parent) as directory:
            download = Path(directory) / "download.tar.gz"
            subprocess.run(
                [
                    "curl",
                    "--fail",
                    "--location",
                    "--silent",
                    "--show-error",
                    "--proto",
                    "=https",
                    "--proto-redir",
                    "=https",
                    "--tlsv1.2",
                    "--max-time",
                    "120",
                    "--max-filesize",
                    str(64 * 1024 * 1024),
                    "--output",
                    str(download),
                    url,
                ],
                check=True,
            )
            verified_binary(download, CHECKSUMS[key])
            os.replace(download, archive)
    binary = verified_binary(archive, CHECKSUMS[key])
    fd, temporary = tempfile.mkstemp(dir=archive.parent)
    try:
        with os.fdopen(fd, "wb") as output:
            output.write(binary)
        os.chmod(temporary, 0o755)
        os.replace(temporary, executable)
    finally:
        Path(temporary).unlink(missing_ok=True)
    print(f"Gitleaks {VERSION}: archive checksum verified", flush=True)


def main():
    if sys.version_info < (3, 12):
        raise ValueError("python_3_12_or_newer_required")
    for command in ("git", "go", "curl"):
        if not shutil.which(command):
            raise ValueError("missing_validation_prerequisite_" + command)
    environment = ROOT / ".venv"
    if not (environment / "bin/python").exists():
        venv.EnvBuilder(with_pip=True).create(environment)
    subprocess.run(
        [
            str(environment / "bin/python"),
            "-m",
            "pip",
            "--isolated",
            "--disable-pip-version-check",
            "install",
            "--no-input",
            "--index-url",
            "https://pypi.org/simple",
            "-r",
            str(ROOT / "requirements-validation.txt"),
        ],
        check=True,
    )
    install_gitleaks()
    module = ROOT / "openshift/deployment/chartassignment-controller"
    goroot = subprocess.check_output(
        ["go", "env", "GOROOT"], cwd=module, text=True
    ).strip()
    subprocess.run(
        [str(Path(goroot) / "bin/go"), "mod", "download"],
        cwd=module,
        env={**os.environ, "GOTOOLCHAIN": "local"},
        check=True,
    )
    # Automatic toolchain selection can require an online checksum lookup.
    (ROOT / ".tools/go-root.txt").write_text(goroot + "\n")
    print("Validation tools ready. Run make validate.", flush=True)


if __name__ == "__main__":
    try:
        main()
    except (
        OSError,
        ValueError,
        KeyError,
        subprocess.SubprocessError,
        tarfile.TarError,
    ):
        print(
            "Validation setup failed; check prerequisites, TLS trust and pinned tool checksums.",
            file=sys.stderr,
        )
        sys.exit(1)
