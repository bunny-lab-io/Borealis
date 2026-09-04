import hashlib
import json
import os
import pathlib
import subprocess
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
RELEASE = "2026.09.1"
SOURCE_SHA = "a" * 40
API_BASE = "https://api.example.test"
REPOSITORY = "bunny-lab-io/Borealis"


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class EngineReleaseBootstrapTests(unittest.TestCase):
    def test_release_packaging_avoids_admin_only_settings_api(self):
        workflow = (
            REPO_ROOT / ".github/workflows/publish-engine-release-assets.yml"
        ).read_text(encoding="utf-8")
        self.assertNotIn("repos/${GITHUB_REPOSITORY}/immutable-releases", workflow)
        self.assertIn("gh release view", workflow)
        self.assertIn("--json isDraft,isPrerelease,tagName", workflow)
        self.assertNotIn("--clobber", workflow)

    def run_engine_library(self, script: str):
        env = os.environ.copy()
        env["BOREALIS_ENGINE_LIBRARY_MODE"] = "1"
        env["BOREALIS_TEST_REPO_ROOT"] = str(REPO_ROOT)
        return subprocess.run(
            ["bash", "-c", f'source "$BOREALIS_TEST_REPO_ROOT/Engine.sh"\n{script}'],
            cwd=REPO_ROOT,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_stable_repo_ref_requires_exact_release_identity(self):
        result = self.run_engine_library(
            "RELEASE_CHANNEL=stable\nREQUESTED_RELEASE=\nREQUESTED_RELEASE_SHA=\nresolve_repo_ref"
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requires exact --release and --release-sha", result.stderr)

        result = self.run_engine_library(
            f"""
RELEASE_CHANNEL=stable
REQUESTED_RELEASE={RELEASE}
REQUESTED_RELEASE_SHA={SOURCE_SHA}
resolve_repo_ref
printf '%s\\n' "$REPO_REF" "$REPO_CHECKOUT_BRANCH"
"""
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout.splitlines(),
            [RELEASE, f"borealis-release-{RELEASE}"],
        )

    def test_unstable_repo_ref_requires_explicit_channel(self):
        result = self.run_engine_library(
            "RELEASE_CHANNEL=unstable\nREQUESTED_RELEASE=\nREQUESTED_RELEASE_SHA=\nresolve_repo_ref\nprintf '%s\\n' \"$REPO_REF\""
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines()[-1], "main")

    def test_stable_repo_sync_verifies_tag_and_checked_out_sha(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            source = root / "source"
            source.mkdir()
            subprocess.run(["git", "init"], cwd=source, check=True, capture_output=True)
            subprocess.run(
                ["git", "config", "user.email", "borealis-tests@example.invalid"],
                cwd=source,
                check=True,
            )
            subprocess.run(
                ["git", "config", "user.name", "Borealis Tests"],
                cwd=source,
                check=True,
            )
            (source / "Engine.sh").write_text("release fixture\n", encoding="utf-8")
            subprocess.run(["git", "add", "Engine.sh"], cwd=source, check=True)
            subprocess.run(
                ["git", "commit", "-m", "release fixture"],
                cwd=source,
                check=True,
                capture_output=True,
            )
            source_sha = subprocess.run(
                ["git", "rev-parse", "HEAD"],
                cwd=source,
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()
            subprocess.run(["git", "tag", RELEASE], cwd=source, check=True)

            install = root / "install"
            result = self.run_engine_library(
                f"""
INSTALL_DIR={install}
REPO_URL=file://{source}
RELEASE_CHANNEL=stable
REQUESTED_RELEASE={RELEASE}
REQUESTED_RELEASE_SHA={source_sha}
run_privileged() {{ "$@"; }}
reconcile_install_checkout_owner() {{ :; }}
restore_selinux_context_if_needed() {{ :; }}
sync_repo
git -C "$INSTALL_DIR" rev-parse HEAD
"""
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines()[-1], source_sha)
            self.assertEqual(
                subprocess.run(
                    ["git", "-C", str(install), "branch", "--show-current"],
                    check=True,
                    capture_output=True,
                    text=True,
                ).stdout.strip(),
                f"borealis-release-{RELEASE}",
            )

            wrong_sha = "b" * 40
            rejected = self.run_engine_library(
                f"""
INSTALL_DIR={root / 'rejected'}
REPO_URL=file://{source}
RELEASE_CHANNEL=stable
REQUESTED_RELEASE={RELEASE}
REQUESTED_RELEASE_SHA={wrong_sha}
run_privileged() {{ "$@"; }}
reconcile_install_checkout_owner() {{ :; }}
restore_selinux_context_if_needed() {{ :; }}
sync_repo
"""
            )
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn("expected", rejected.stderr)

    def make_release_fixture(self, root: pathlib.Path, *, immutable=True):
        source = root / "source"
        source.mkdir()
        engine = source / "Engine.sh"
        engine.write_text(
            "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > \"$BOREALIS_TEST_CAPTURE\"\n",
            encoding="utf-8",
        )
        engine.chmod(0o755)

        artifacts = root / "artifacts"
        subprocess.run(
            [
                "python3",
                str(REPO_ROOT / "Tests/tools/build_engine_release_assets.py"),
                "--release",
                RELEASE,
                "--repository",
                REPOSITORY,
                "--source-sha",
                SOURCE_SHA,
                "--engine-script",
                str(engine),
                "--bootstrap-script",
                str(REPO_ROOT / "Install-Engine.sh"),
                "--output-dir",
                str(artifacts),
            ],
            check=True,
            cwd=REPO_ROOT,
        )

        names = (
            "Install-Engine.sh",
            "Engine.sh",
            "borealis-engine-install-manifest.json",
            "SHA256SUMS",
        )
        assets = []
        for asset_id, name in enumerate(names, start=1):
            path = artifacts / name
            assets.append(
                {
                    "name": name,
                    "state": "uploaded",
                    "digest": f"sha256:{sha256(path)}",
                    "url": f"{API_BASE}/repos/{REPOSITORY}/releases/assets/{asset_id}",
                }
            )
        release_json = root / "release.json"
        release_json.write_text(
            json.dumps(
                {
                    "tag_name": RELEASE,
                    "draft": False,
                    "prerelease": False,
                    "immutable": immutable,
                    "assets": assets,
                }
            ),
            encoding="utf-8",
        )
        tag_json = root / "tag.json"
        tag_json.write_text(
            json.dumps(
                {
                    "object": {
                        "type": "commit",
                        "sha": SOURCE_SHA,
                        "url": f"{API_BASE}/repos/{REPOSITORY}/git/commits/{SOURCE_SHA}",
                    }
                }
            ),
            encoding="utf-8",
        )

        fake_bin = root / "bin"
        fake_bin.mkdir()
        fake_curl = fake_bin / "curl"
        fake_curl.write_text(
            """#!/usr/bin/env bash
set -eu
url=""
output=""
while (($#)); do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */releases/tags/*) source="$BOREALIS_TEST_RELEASE_JSON" ;;
  */git/ref/tags/*) source="$BOREALIS_TEST_TAG_JSON" ;;
  */releases/assets/2) source="$BOREALIS_TEST_ARTIFACTS/Engine.sh" ;;
  */releases/assets/3) source="$BOREALIS_TEST_ARTIFACTS/borealis-engine-install-manifest.json" ;;
  *) printf 'unexpected curl URL: %s\\n' "$url" >&2; exit 70 ;;
esac
cp "$source" "$output"
""",
            encoding="utf-8",
        )
        fake_curl.chmod(0o755)
        return artifacts, fake_bin, release_json, tag_json

    def invoke_bootstrap(
        self,
        root: pathlib.Path,
        artifacts: pathlib.Path,
        fake_bin: pathlib.Path,
        release_json: pathlib.Path,
        tag_json: pathlib.Path,
    ):
        capture = root / "engine-args"
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{fake_bin}{os.pathsep}{env['PATH']}",
                "BOREALIS_GITHUB_API_BASE_URL": API_BASE,
                "BOREALIS_GITHUB_REPOSITORY": REPOSITORY,
                "BOREALIS_TEST_RELEASE_JSON": str(release_json),
                "BOREALIS_TEST_TAG_JSON": str(tag_json),
                "BOREALIS_TEST_ARTIFACTS": str(artifacts),
                "BOREALIS_TEST_CAPTURE": str(capture),
            }
        )
        result = subprocess.run(
            [
                "bash",
                str(artifacts / "Install-Engine.sh"),
                "--release",
                RELEASE,
                "--network-mode",
                "local",
                "--install-dir",
                str(root / "install"),
            ],
            cwd=root,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        return result, capture

    def run_bootstrap(self, root: pathlib.Path, *, immutable=True):
        artifacts, fake_bin, release_json, tag_json = self.make_release_fixture(
            root, immutable=immutable
        )
        result, capture = self.invoke_bootstrap(
            root, artifacts, fake_bin, release_json, tag_json
        )
        return result, capture, artifacts

    def test_verified_bootstrap_passes_exact_identity_to_engine(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            result, capture, artifacts = self.run_bootstrap(root)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn(
                f"Verified immutable Borealis Engine release {RELEASE} at {SOURCE_SHA}.",
                result.stdout,
            )
            self.assertEqual(
                capture.read_text(encoding="utf-8").splitlines(),
                [
                    "--install-dir",
                    str(root / "install"),
                    "--repo-url",
                    "https://github.com/bunny-lab-io/Borealis.git",
                    "--release",
                    RELEASE,
                    "--release-sha",
                    SOURCE_SHA,
                    "--network-mode",
                    "local",
                    "deploy",
                    "prod",
                ],
            )
            manifest = json.loads(
                (artifacts / "borealis-engine-install-manifest.json").read_text(
                    encoding="utf-8"
                )
            )
            self.assertEqual(manifest["source_sha"], SOURCE_SHA)
            self.assertIn("linux-amd64", manifest["supported_platforms"])
            self.assertEqual(
                manifest["assets"]["engine"]["sha256"],
                sha256(artifacts / "Engine.sh"),
            )
            self.assertEqual(
                manifest["assets"]["engine"]["url"],
                f"https://github.com/{REPOSITORY}/releases/download/{RELEASE}/Engine.sh",
            )

    def test_bootstrap_rejects_mutable_release(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            result, capture, _ = self.run_bootstrap(
                pathlib.Path(temp_dir), immutable=False
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(capture.exists())
            self.assertIn("requested GitHub release is not immutable", result.stderr)

    def test_bootstrap_rejects_engine_checksum_mismatch(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            artifacts, fake_bin, release_json, tag_json = self.make_release_fixture(root)
            (artifacts / "Engine.sh").write_text("tampered\n", encoding="utf-8")
            result, _ = self.invoke_bootstrap(
                root, artifacts, fake_bin, release_json, tag_json
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Engine.sh checksum verification failed", result.stderr)

    def test_bootstrap_rejects_tag_release_mismatch(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            artifacts, fake_bin, release_json, tag_json = self.make_release_fixture(root)
            payload = json.loads(release_json.read_text(encoding="utf-8"))
            payload["tag_name"] = "2026.09.2"
            release_json.write_text(json.dumps(payload), encoding="utf-8")
            result, capture = self.invoke_bootstrap(
                root, artifacts, fake_bin, release_json, tag_json
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(capture.exists())
            self.assertIn("release tag does not match", result.stderr)

    def test_bootstrap_rejects_unavailable_release_metadata(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            artifacts, fake_bin, release_json, tag_json = self.make_release_fixture(root)
            release_json.unlink()
            result, capture = self.invoke_bootstrap(
                root, artifacts, fake_bin, release_json, tag_json
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(capture.exists())
            self.assertIn("Cannot read published GitHub release", result.stderr)


if __name__ == "__main__":
    unittest.main()
