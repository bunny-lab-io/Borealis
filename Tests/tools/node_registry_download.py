"""Public, digest-addressed release input reads; no operator registry credentials."""

import hashlib
import json
from pathlib import Path
import re
import time
import urllib.error
import urllib.parse
import urllib.request

import resolve_node_external_images as pins


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


REGISTRIES = {
    "docker.io": ("registry-1.docker.io", "https://auth.docker.io/token", "registry.docker.io"),
    "ghcr.io": ("ghcr.io", "https://ghcr.io/token", "ghcr.io"),
    "quay.io": ("quay.io", "https://quay.io/v2/auth", "quay.io"),
    "registry.k8s.io": ("registry.k8s.io", None, None),
}


def allowed_redirect(registry: str, url: str) -> bool:
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https" or parsed.port not in (None, 443) or parsed.username or parsed.password or parsed.fragment:
        return False
    host = parsed.hostname or ""
    if host == REGISTRIES[registry][0]:
        return True
    if registry == "ghcr.io":
        return host == "pkg-containers.githubusercontent.com"
    if registry == "quay.io":
        return re.fullmatch(r"cdn[0-9]*\.quay\.io", host) is not None
    if registry == "docker.io":
        return host in ("production.cloudflare.docker.com", "production.cloudfront.docker.com") or re.fullmatch(r"docker-images-prod\.[0-9a-f]{32}\.r2\.cloudflarestorage\.com", host) is not None
    return host == "cdn.registry.k8s.io" or re.fullmatch(r"[a-z]+-[a-z]+[0-9]-docker\.pkg\.dev", host) is not None


def read_bounded(response, limit: int, deadline: float, consume):
    size = 0
    while True:
        if time.monotonic() >= deadline:
            raise ValueError("public registry input deadline exceeded")
        chunk = response.read1(min(1 << 20, limit + 1 - size))
        if not chunk:
            return size
        size += len(chunk)
        if size > limit:
            raise ValueError("public registry input exceeds source bound")
        consume(chunk)


class RegistryReader:
    def __init__(self, reference: str):
        if not (pins.inputs.REFERENCE.fullmatch(reference) or pins.inputs.DIGEST_REFERENCE.fullmatch(reference)):
            raise ValueError("unsupported public image reference")
        repository = reference.split("@", 1)[0].split(":", 1)[0]
        self.registry, self.repository = repository.split("/", 1)
        self.host, self.token_url, self.service = REGISTRIES[self.registry]
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        self.token = None

    def authenticate(self):
        if not self.token_url:
            raise ValueError("public registry authentication unavailable")
        query = urllib.parse.urlencode({"service": self.service, "scope": "repository:" + self.repository + ":pull"})
        with self.opener.open(urllib.request.Request(self.token_url + "?" + query), timeout=30) as response:
            if response.status != 200 or response.headers.get("Content-Encoding") not in (None, "identity"):
                raise ValueError("public registry token response invalid")
            parts = []
            read_bounded(response, 32768, time.monotonic() + 60, parts.append)
        value = json.loads(b"".join(parts), object_pairs_hook=pins.unique_object)
        token = value.get("token", value.get("access_token"))
        if not isinstance(token, str) or not 1 <= len(token) <= 16384 or any(ord(c) < 33 or ord(c) > 126 for c in token):
            raise ValueError("public registry token invalid")
        self.token = token

    def fetch(self, digest: str, size: int, destination: Path, *, manifest=False):
        if not pins.DIGEST.fullmatch(digest) or type(size) is not int or not 0 < size < 2 << 30:
            raise ValueError("invalid pinned registry input")
        endpoint = "manifests" if manifest else "blobs"
        url = "https://" + self.host + "/v2/" + self.repository + "/" + endpoint + "/" + digest
        authenticated, redirected = False, False
        deadline = time.monotonic() + 300
        try:
            for attempt in range(7):
                if time.monotonic() >= deadline:
                    raise ValueError("registry input deadline exceeded")
                headers = {"Accept-Encoding": "identity"}
                if manifest:
                    headers["Accept"] = ", ".join(sorted(pins.INDEX_TYPES | pins.MANIFEST_TYPES))
                # Once redirected, credentials never follow, even back to the
                # registry. A signed CDN URL is only an unauthenticated GET.
                if self.token and not redirected:
                    headers["Authorization"] = "Bearer " + self.token
                try:
                    response = self.opener.open(urllib.request.Request(url, headers=headers), timeout=30)
                except urllib.error.HTTPError as error:
                    with error:
                        if error.code == 401 and not authenticated and not redirected:
                            self.authenticate()
                            authenticated = True
                            continue
                        if error.code in (301, 302, 303, 307, 308):
                            next_url = urllib.parse.urljoin(url, error.headers.get("Location", ""))
                            if not allowed_redirect(self.registry, next_url):
                                raise ValueError("unapproved public registry redirect")
                            url, redirected = next_url, True
                            continue
                    raise ValueError("public registry input unavailable") from None
                with response:
                    if response.status != 200 or response.headers.get("Content-Encoding") not in (None, "identity"):
                        raise ValueError("public registry input response invalid")
                    length = response.headers.get("Content-Length")
                    if length is not None and length != str(size):
                        raise ValueError("public registry input length changed")
                    digestor = hashlib.sha256()
                    with destination.open("xb") as output:
                        def write(chunk):
                            digestor.update(chunk)
                            output.write(chunk)
                        received = read_bounded(response, size, deadline, write)
                    if received != size or "sha256:" + digestor.hexdigest() != digest:
                        raise ValueError("public registry input content changed")
                return
            raise ValueError("public registry redirect bound exceeded")
        except Exception:
            # Never expose tokens, response bodies, signed CDN URLs or raw
            # urllib diagnostics. Caller owns cleanup of private partial files.
            raise ValueError("pinned public registry download failed") from None

    def close(self):
        self.token = None
