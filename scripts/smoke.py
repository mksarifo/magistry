#!/usr/bin/env python3
"""Exercise the running registry with real OCI uploads and digest deletion.

Use a disposable registry: this creates a unique repository and deletes its
manifests. Distribution may retain the empty repository in its catalog.
Usage: python3 scripts/smoke.py http://127.0.0.1:5051
Optional credentials: SMOKE_USERNAME / SMOKE_PASSWORD environment variables.
"""

import base64
import hashlib
import json
import os
import sys
import uuid
import time
from urllib.error import HTTPError
from urllib.parse import urlencode, urljoin, urlparse
from urllib.request import Request, urlopen


origin = sys.argv[1].rstrip("/") if len(sys.argv) > 1 else "http://127.0.0.1:5051"
repository = "smoke/" + uuid.uuid4().hex[:12]
credentials = f"{os.getenv('SMOKE_USERNAME', '')}:{os.getenv('SMOKE_PASSWORD', '')}"
headers = {"Accept": "application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json"}
if credentials != ":":
    headers["Authorization"] = "Basic " + base64.b64encode(credentials.encode()).decode()


def request(method, path, data=None, content_type=None, expected=200):
    target = urljoin(origin, path)
    assert urlparse(target).netloc == urlparse(origin).netloc, target
    outgoing = dict(headers)
    if content_type:
        outgoing["Content-Type"] = content_type
    try:
        with urlopen(Request(target, data=data, headers=outgoing, method=method), timeout=30) as response:
            assert response.status == expected, (response.status, expected)
            return response.read(), response.headers
    except HTTPError as error:
        if error.code == expected:
            return error.read(), error.headers
        raise AssertionError(f"{method} {path}: {error.code} {error.read().decode()}") from error


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def upload(data):
    _, response_headers = request("POST", f"/v2/{repository}/blobs/uploads/", b"", expected=202)
    location = response_headers["Location"]
    # Exercise the resumable PATCH path before completing the blob.
    _, response_headers = request("PATCH", location, data, "application/octet-stream", 202)
    location = response_headers["Location"]
    separator = "&" if "?" in location else "?"
    request("PUT", location + separator + urlencode({"digest": digest(data)}), b"", "application/octet-stream", 201)
    downloaded, _ = request("GET", f"/v2/{repository}/blobs/{digest(data)}")
    assert downloaded == data, "Blob contents changed through the proxy"
    return {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": digest(data), "size": len(data)}


request("GET", "/healthz")
if os.getenv("SMOKE_BOOTSTRAP") == "1":
    session, _ = request("GET", "/api/auth/session")
    assert json.loads(session)["setupRequired"], "Use a new disposable registry for bootstrap tests"
    body = json.dumps({"username": os.environ["SMOKE_USERNAME"], "password": os.environ["SMOKE_PASSWORD"]}).encode()
    _, login_headers = request("POST", "/api/auth/setup", body, "application/json")
    assert "HttpOnly" in login_headers["Set-Cookie"] and "SameSite=Strict" in login_headers["Set-Cookie"]
request("GET", "/api/status")
request("GET", "/v2/")
config = json.dumps({"architecture": "arm64", "os": "linux", "created": "2026-01-01T00:00:00Z", "rootfs": {"type": "layers", "diff_ids": []}}).encode()
descriptor = upload(config)
media_type = "application/vnd.oci.image.manifest.v1+json"
manifest = json.dumps({"schemaVersion": 2, "mediaType": media_type, "config": descriptor, "layers": []}).encode()
for tag in ("latest", "v1"):
    request("PUT", f"/v2/{repository}/manifests/{tag}", manifest, media_type, 201)
data, _ = request("GET", "/api/repositories")
assert repository in json.loads(data)["repositories"]
data, _ = request("GET", "/api/tags?" + urlencode({"repository": repository}))
tags = json.loads(data)["tags"]
assert {tag["name"] for tag in tags} == {"latest", "v1"}, tags
assert all(tag["digest"] == digest(manifest) and tag["platforms"] == ["linux/arm64"] and not tag.get("error") for tag in tags), tags
for tag in ("latest", "v1"):
    request("HEAD", f"/v2/{repository}/manifests/{tag}")
request("DELETE", "/api/manifests?" + urlencode({"repository": repository, "digest": digest(manifest)}), expected=204)
for tag in ("latest", "v1"):
    request("GET", f"/v2/{repository}/manifests/{tag}", expected=404)
data, _ = request("GET", "/api/tags?" + urlencode({"repository": repository}))
assert json.loads(data)["tags"] == []
print("PASS: resumable blob upload, byte-for-byte download, manifests, catalog, metadata, aliases, and digest deletion")

if os.getenv("SMOKE_STORAGE") == "1":
    # Keep a tagged manifest sharing the existing configuration blob. Cleanup
    # must retain these while removing an unrelated uploaded orphan blob.
    request("PUT", f"/v2/{repository}/manifests/keep", manifest, media_type, 201)
    orphan = upload(b"orphan-test-data-" + os.urandom(128 * 1024))
    def usage():
        body, _ = request("GET", "/api/storage")
        return json.loads(body)
    before = usage()["usage"]["usedBytes"]
    request("PUT", "/api/storage/limit", json.dumps({"limitBytes": before}).encode(), "application/json", 204)
    request("POST", f"/v2/{repository}/blobs/uploads/", b"", expected=413)
    request("GET", f"/v2/{repository}/manifests/keep")
    request("PUT", "/api/storage/limit", b'{"limitBytes":0}', "application/json", 204)
    def collect(dry_run):
        request("POST", "/api/storage/cleanup", json.dumps({"dryRun": dry_run, "confirm": not dry_run}).encode(), "application/json", 202)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            state = usage()
            if state["job"]["state"] != "running":
                assert state["job"]["state"] == "done", state
                return state
            time.sleep(0.1)
        raise AssertionError("Storage cleanup did not finish")
    preview = collect(True)
    assert preview["usage"]["usedBytes"] == before, "Dry run removed data"
    result = collect(False)
    assert result["job"]["reclaimedBytes"] >= orphan["size"], result
    request("GET", f"/v2/{repository}/blobs/{orphan['digest']}", expected=404)
    request("GET", f"/v2/{repository}/manifests/keep")
    request("GET", f"/v2/{repository}/blobs/{descriptor['digest']}")
    print("PASS: administrator bootstrap, soft quota, non-destructive analysis, real garbage collection, and retained images")
