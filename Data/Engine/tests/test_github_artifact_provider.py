import tempfile
from pathlib import Path
from unittest import TestCase, mock

from Data.Engine.integrations.github.artifact_provider import GitHubArtifactProvider


class GitHubArtifactProviderProxyTests(TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.cache_file = Path(self._tmp.name) / "cache.json"

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def _provider(self, **kwargs) -> GitHubArtifactProvider:
        return GitHubArtifactProvider(
            cache_file=self.cache_file,
            default_repo="owner/repo",
            default_branch="main",
            refresh_interval=60,
            **kwargs,
        )

    def test_fetch_repo_head_bypasses_proxies_by_default(self) -> None:
        provider = self._provider()

        fake_response = mock.Mock()
        fake_response.status_code = 200
        fake_response.json.return_value = {"commit": {"sha": "abc123"}}

        with mock.patch("Data.Engine.integrations.github.artifact_provider.requests") as fake_requests:
            fake_requests.get.return_value = fake_response

            snapshot = provider.refresh_default_repo_head(force=True)

        fake_requests.get.assert_called_once()
        _, kwargs = fake_requests.get.call_args
        assert kwargs.get("proxies") == {}
        self.assertEqual(snapshot.sha, "abc123")
        self.assertFalse(snapshot.cached)

    def test_fetch_repo_head_respects_proxy_opt_in(self) -> None:
        provider = self._provider(allow_proxies=True)

        fake_response = mock.Mock()
        fake_response.status_code = 200
        fake_response.json.return_value = {"commit": {"sha": "def456"}}

        with mock.patch("Data.Engine.integrations.github.artifact_provider.requests") as fake_requests:
            fake_requests.get.return_value = fake_response

            snapshot = provider.refresh_default_repo_head(force=True)

        fake_requests.get.assert_called_once()
        _, kwargs = fake_requests.get.call_args
        self.assertNotIn("proxies", kwargs)
        self.assertEqual(snapshot.sha, "def456")
        self.assertFalse(snapshot.cached)

    def test_verify_token_bypasses_proxies(self) -> None:
        provider = self._provider()

        fake_response = mock.Mock()
        fake_response.status_code = 200
        fake_response.json.return_value = {"resources": {"core": {"limit": 1, "remaining": 1, "reset": 1, "used": 0}}}

        with mock.patch("Data.Engine.integrations.github.artifact_provider.requests") as fake_requests:
            fake_requests.get.return_value = fake_response

            status = provider.verify_token("token")

        fake_requests.get.assert_called_once()
        _, kwargs = fake_requests.get.call_args
        assert kwargs.get("proxies") == {}
        self.assertTrue(status.valid)

