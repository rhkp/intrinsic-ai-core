# This project was developed with assistance from AI tools.
"""Exercise failures without leaking fixture credentials or touching a live cluster."""

import contextlib
import hashlib
import io
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
import setup_validation as tools
import validation


class ValidationTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(
            prefix="intrinsic-validation-test-"
        )
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name).resolve()
        subprocess.run(["git", "init", "--quiet", str(self.root)], check=True)
        self.write(".gitignore", ".env\n.generated/\n*.json\n")
        self.write(
            "openshift/deployment/SOURCE_MANIFEST.json", json.dumps({"files": []})
        )

    def write(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        return path

    def credential(self):
        # Inert, assembled only in temporary fixtures; never a real credential.
        return "ghp_" + "Ab3dEf6hJk9mNp2rSt5vWx8zBc1fGh4jKl7n"

    def test_current_untracked_files_are_scanned_while_ignored_files_and_git_metadata_are_not(
        self,
    ):
        self.write(".env", "TOKEN=" + self.credential())
        self.write(".generated/evidence.txt", self.credential())
        self.write(".git/not-a-history-scan.txt", self.credential())
        self.write("notes.txt", "Public documentation.\n")
        files = validation.repository_files(self.root)
        self.assertIn(Path("notes.txt"), files)
        self.assertNotIn(Path(".env"), files)
        self.assertFalse(any(path.parts[0] in {".generated", ".git"} for path in files))
        validation.scan_secrets(files, self.root)

    def test_forced_tracked_env_is_rejected_before_reading_contents(self):
        for name in (".env", "local.env", "id_ed25519", "private.der"):
            with self.subTest(name=name):
                source = self.write(name, "TOKEN=" + self.credential())
                subprocess.run(
                    ["git", "add", "--force", name], cwd=self.root, check=True
                )
                with self.assertRaisesRegex(
                    validation.ValidationError, "private_file_selected"
                ):
                    validation.repository_files(self.root)
                subprocess.run(
                    ["git", "rm", "--cached", "--force", "--quiet", "--", name],
                    cwd=self.root,
                    check=True,
                )
                source.unlink()

    def test_sample_placeholders_pass_but_credential_shaped_values_and_inline_suppression_fail(
        self,
    ):
        self.write(".env.sample", "API_TOKEN=replace-with-your-token\n")
        validation.scan_secrets(validation.repository_files(self.root), self.root)
        self.write(
            ".env.sample", "API_TOKEN=" + self.credential() + " # gitleaks:allow\n"
        )
        output = io.StringIO()
        with (
            contextlib.redirect_stderr(output),
            self.assertRaises(validation.ValidationError),
        ):
            validation.scan_secrets(validation.repository_files(self.root), self.root)
        self.assertIn('"rule"', output.getvalue())
        self.assertNotIn(self.credential(), output.getvalue())
        self.assertNotIn("API_TOKEN=", output.getvalue())

    def test_symlinks_cannot_read_files_outside_the_snapshot(self):
        self.write(".generated/private.txt", self.credential())
        (self.root / "linked.txt").symlink_to(self.root / ".generated/private.txt")
        with self.assertRaisesRegex(validation.ValidationError, "symlinks"):
            validation.repository_files(self.root)

    def test_python_errors_return_file_and_rule_without_echoing_source(self):
        self.write("broken.py", "value = 'private-fixture-value'\nif :\n")
        output = io.StringIO()
        with (
            contextlib.redirect_stderr(output),
            self.assertRaises(validation.ValidationError),
        ):
            validation.lint_python([Path("broken.py")], self.root)
        self.assertIn("broken.py", output.getvalue())
        self.assertNotIn("private-fixture-value", output.getvalue())

    def test_yaml_duplicate_keys_and_invalid_syntax_are_errors(self):
        for content in ("key: one\nkey: two\n", "key: [\n"):
            with self.subTest(content=content):
                self.write("broken.yaml", content)
                with (
                    contextlib.redirect_stderr(io.StringIO()),
                    self.assertRaises(validation.ValidationError),
                ):
                    validation.lint_yaml([Path("broken.yaml")], self.root)

    def test_shell_syntax_errors_are_rejected(self):
        self.write("broken.sh", "if then\n")
        with (
            contextlib.redirect_stderr(io.StringIO()),
            self.assertRaises(validation.ValidationError),
        ):
            validation.lint_shell([Path("broken.sh")], self.root)

    def test_go_formatting_is_checked_without_rewriting_source(self):
        self.write("go.mod", "module example.invalid/validation-test\n\ngo 1.22.0\n")
        source = self.write("broken.go", "package main\nfunc main(){ }\n")
        before = source.read_bytes()
        with (
            contextlib.redirect_stderr(io.StringIO()),
            self.assertRaises(validation.ValidationError),
        ):
            validation.lint_go([Path("broken.go")], self.root, self.root)
        self.assertEqual(source.read_bytes(), before)

    def test_staged_whitespace_errors_are_rejected(self):
        self.write("notes.txt", "bad trailing space \n")
        subprocess.run(["git", "add", "notes.txt"], cwd=self.root, check=True)
        with (
            contextlib.redirect_stderr(io.StringIO()),
            self.assertRaises(validation.ValidationError),
        ):
            validation.run(["git", "diff", "--cached", "--check"], cwd=self.root)

    def test_source_provenance_digest_drift_is_rejected(self):
        sys.path.insert(0, str(validation.ROOT / "openshift"))
        from validate_source_manifest import ValidationError, verify_file

        source = self.write("source.txt", "changed content")
        with self.assertRaises(ValidationError):
            verify_file(source, "0" * 64, "fixture", self.root)

    def test_nonzero_check_stops_the_top_level_validation(self):
        calls = []

        def failed():
            calls.append("failed")
            raise validation.ValidationError("intentional_test_failure")

        with (
            patch.object(validation, "check_tools"),
            patch.object(validation, "repository_files", return_value=[]),
            patch.object(
                validation,
                "checks",
                return_value={"first": failed, "later": lambda: calls.append("later")},
            ),
            contextlib.redirect_stdout(io.StringIO()),
            contextlib.redirect_stderr(io.StringIO()),
        ):
            self.assertEqual(validation.main(["validate"]), 1)
        self.assertEqual(calls, ["failed"])

    def test_corrupted_tool_archive_and_unexpected_member_fail_closed(self):
        archive = self.root / "tool.tar.gz"
        archive.write_bytes(b"not the official archive")
        with self.assertRaisesRegex(ValueError, "checksum_mismatch"):
            tools.verified_binary(archive, "0" * 64)
        with tarfile.open(archive, "w:gz") as tar:
            member = tarfile.TarInfo("gitleaks")
            member.type = tarfile.SYMTYPE
            member.linkname = "outside"
            tar.addfile(member)
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        with self.assertRaisesRegex(ValueError, "archive_member"):
            tools.verified_binary(archive, digest)


if __name__ == "__main__":
    unittest.main()
