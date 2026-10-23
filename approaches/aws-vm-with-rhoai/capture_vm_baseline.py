# This project was developed with assistance from AI tools.
"""Run on the VM to back up source and inference ownership without touching caches."""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile


def command(args, cwd=None):
    result = subprocess.run(args, cwd=cwd, capture_output=True, timeout=120)
    if result.returncode:
        raise RuntimeError(f"Baseline command failed: {args[0]}; details suppressed")
    return result.stdout


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def identity(path):
    stat = path.stat()
    return {"path": str(path), "device": stat.st_dev, "inode": stat.st_ino}


def cache_state(home):
    result = {}
    for name in ("intrinsic-core", "intrinsic-omts"):
        root = home / name
        links = {}
        for label in ("bazel-bin", "bazel-out", "bazel-testlogs", "bazel-" + name):
            link = root / label
            if link.is_symlink():
                links[label] = identity(link.resolve())
        result[name] = links
    for name, path in (
        ("bazel_user_root", home / ".cache/bazel/_bazel_ubuntu"),
        ("distfiles", home / "intrinsic-distfiles"),
    ):
        if path.exists():
            result[name] = identity(path)
    return result


def capture(output):
    home = Path.home()
    output = output.expanduser().resolve()
    protected = [
        home / name
        for name in (
            "intrinsic-core",
            "intrinsic-omts",
            "intrinsic-distfiles",
            ".cache/bazel",
        )
    ]
    if any(output == root or output.is_relative_to(root) for root in protected):
        raise ValueError("Store the backup outside source trees and compile caches")
    output.mkdir(parents=True, mode=0o700, exist_ok=False)
    os.chmod(output, 0o700)
    previous_umask = os.umask(0o077)
    try:
        before = cache_state(home)
        report = {
            "timestamp_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "repositories": {},
            "cache_state": before,
            "runtime_objects": [],
        }
        for name in ("intrinsic-core", "intrinsic-omts"):
            root = home / name
            destination = output / name
            destination.mkdir(mode=0o700)

            def git(*args):
                return command(["git", *args], cwd=root)

            head = git("rev-parse", "HEAD").decode().strip()
            for filename, args in {
                "status.z": ["status", "--porcelain=v1", "-z"],
                "changes-from-head.patch": ["diff", "--binary", "HEAD"],
                "staged.patch": ["diff", "--cached", "--binary"],
                "unstaged.patch": ["diff", "--binary"],
                "head": ["rev-parse", "HEAD"],
                "branch": ["branch", "--show-current"],
                "worktrees.txt": ["worktree", "list", "--porcelain"],
            }.items():
                (destination / filename).write_bytes(git(*args))
            git(
                "bundle", "create", str(destination / "history.bundle"), "--all", "HEAD"
            )
            git("bundle", "verify", str(destination / "history.bundle"))
            tracked = git("ls-files", "-z").split(b"\0")
            untracked = git("ls-files", "--others", "--exclude-standard", "-z").split(
                b"\0"
            )
            names = sorted({os.fsdecode(item) for item in tracked + untracked if item})
            files, missing = {}, []
            with tarfile.open(
                destination / "working-tree.tar.gz", "w:gz", dereference=False
            ) as archive:
                for relative in names:
                    path = root / relative
                    if path.is_symlink():
                        files[relative] = {"symlink": os.readlink(path)}
                    elif path.is_file():
                        files[relative] = {
                            "sha256": digest(path),
                            "mode": path.stat().st_mode & 0o777,
                        }
                    else:
                        missing.append(relative)
                        continue
                    archive.add(path, arcname=relative, recursive=False)
            # Verify archived bytes and symlink targets against the baseline manifest.
            with tarfile.open(destination / "working-tree.tar.gz", "r:gz") as archive:
                for member in archive.getmembers():
                    expected = files[member.name]
                    if member.issym():
                        if expected != {"symlink": member.linkname}:
                            raise RuntimeError("Source symlink backup mismatch")
                    elif member.isfile():
                        with archive.extractfile(member) as data:
                            if (
                                hashlib.file_digest(data, "sha256").hexdigest()
                                != expected["sha256"]
                            ):
                                raise RuntimeError("Source file backup mismatch")
                    elif member.islnk():
                        # tar may deduplicate tracked hard links; verify their bytes too.
                        with archive.extractfile(member) as data:
                            if (
                                hashlib.file_digest(data, "sha256").hexdigest()
                                != expected["sha256"]
                            ):
                                raise RuntimeError("Source hard-link backup mismatch")
                    else:
                        raise RuntimeError("Unexpected source backup entry")
            (destination / "files.json").write_text(
                json.dumps({"files": files, "missing": missing}, indent=2) + "\n"
            )
            changed = [
                os.fsdecode(item)
                for item in git("diff", "--name-only", "-z", "HEAD").split(b"\0")
                if item
            ]
            report["repositories"][name] = {
                "head": head,
                "changed_tracked_files": changed,
                "untracked_files": [os.fsdecode(item) for item in untracked if item],
                "source_entries_backed_up": len(files),
                "bundle_verified": True,
                "source_archive_verified": True,
            }
        runtime = output / "runtime"
        runtime.mkdir(mode=0o700)
        queries = {
            "inference-statefulset": [
                "statefulset",
                "rs-inference-service",
                "-n",
                "app-resources",
            ],
            "inference-pod": ["pod", "rs-inference-service-0", "-n", "app-resources"],
            "chartassignments": ["chartassignments", "-A"],
            "resourcesets": ["resourcesets", "-A"],
        }
        statefulset = None
        for filename, args in queries.items():
            data = command(["kubectl", "get", *args, "-o", "json"])
            (runtime / (filename + ".json")).write_bytes(data)
            report["runtime_objects"].append(filename)
            if filename == "inference-statefulset":
                statefulset = json.loads(data)
        for volume in statefulset["spec"]["template"]["spec"].get("volumes", []):
            if "configMap" in volume:
                name = volume["configMap"]["name"]
                data = command(
                    [
                        "kubectl",
                        "get",
                        "configmap",
                        name,
                        "-n",
                        "app-resources",
                        "-o",
                        "json",
                    ]
                )
                (runtime / ("configmap-" + name + ".json")).write_bytes(data)
        after = cache_state(home)
        if before != after:
            raise RuntimeError(
                "Compile cache identities changed during baseline capture"
            )
        report["cache_identities_unchanged"] = True
        checksums = {
            str(path.relative_to(output)): digest(path)
            for path in output.rglob("*")
            if path.is_file()
        }
        report["backup_sha256"] = checksums
        (output / "manifest.json").write_text(json.dumps(report, indent=2) + "\n")
        for relative, expected in checksums.items():
            if digest(output / relative) != expected:
                raise RuntimeError("Backup integrity check failed")
        print(
            json.dumps(
                {
                    "backup": str(output),
                    "repositories": report["repositories"],
                    "runtime_snapshots": len(report["runtime_objects"]),
                    "cache_identities_unchanged": True,
                    "backup_checksums_verified": True,
                }
            )
        )
    finally:
        os.umask(previous_umask)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    capture(args.output)
