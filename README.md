# Magistry

A small, self-hosted Docker registry with a Go management API and a React / TypeScript interface. Docker push and pull, the API, and the UI share **one port**. Image storage is handled by [CNCF Distribution](https://distribution.github.io/distribution/), the registry implementation written in Go.

## Run in Docker

Requires Docker Engine or Docker Desktop with Docker Compose.

```sh
cp .env.example .env
docker compose up --build -d
```

Open **http://localhost:5050** and create your administrator account. Use a password of at least 12 characters. Then log in from Docker and push an image:

```sh
docker pull alpine:3.23
docker login localhost:5050
docker tag alpine:3.23 localhost:5050/alpine:latest
docker push localhost:5050/alpine:latest
docker pull localhost:5050/alpine:latest
```

Refresh the library after a push. Nested repositories such as `team/my-app` work too. The UI shows tags, digests, compressed layer sizes, creation dates when present, and platforms. Multi-platform indexes show all advertised image platforms; they do not have a single creation date. Metadata errors appear on the affected tag.

No images are seeded by default. Images survive container restarts and `docker compose down` through the `registry-data` volume. The administrator account and storage limit persist separately in `app-data`. `docker compose down -v` permanently deletes both volumes.

## Authentication and remote access

The default port binds to `127.0.0.1`. Authentication is always required for the management API and Docker registry. The first-run screen creates one administrator; public account registration closes immediately afterward. Accounts created in the UI store only salted PBKDF2-HMAC-SHA256 password hashes with 600,000 iterations. Browser sign-in uses a random, revocable HttpOnly / SameSite=Strict cookie with a 24-hour lifetime. Sign-out revokes the session, and app restarts end browser sessions. Docker uses HTTP Basic authentication with the same administrator credentials. Failed login attempts are rate limited.

For deployment-managed credentials or account recovery, set both `AUTH_USERNAME` and `AUTH_PASSWORD` in `.env`, then run `docker compose up -d`. These override the stored administrator while configured; removing them restores the stored account. There are no roles or per-repository permissions. Back up both volumes, including the administrator account, before maintenance.

For remote access, complete administrator setup locally first, put the app behind an HTTPS reverse proxy, set `COOKIE_SECURE=true`, change `BIND_ADDRESS` as needed, and set `PUBLIC_REGISTRY` to the external host (and port, if nonstandard), without `https://`. Configure the proxy to preserve the external Host header, allow large request bodies, and use long streaming timeouts. The internal registry container has no published host port.

Localhost works over HTTP with Docker. For a trusted LAN using HTTP, Docker clients need the address added to their daemon's `insecure-registries` configuration. Use HTTPS for remote deployments, especially with Basic authentication. See [Docker's registry configuration guide](https://docs.docker.com/registry/insecure/).

## Storage management

Open **Storage** in the sidebar to see registry data size, unique blob counts, pending uploads, and free space on the Docker host filesystem. Registry usage sums stored file sizes; shared blobs count once. Filesystem free space is shared with other Docker data and is not a dedicated volume capacity.

Set a **storage limit in GiB** to stop new blob uploads when usage reaches the configured threshold. Leave it blank or set 0 for unlimited storage. Limits survive restarts. This is a soft threshold, not a hard filesystem quota: concurrent or unknown-length requests can exceed it. Pulls and deletions remain available when the limit is reached.

Use **Analyze unused data** for a non-destructive garbage-collection report. Review it, then choose **Reclaim unused blobs** and confirm. The app pauses registry traffic for the entire job, waits for active registry requests to finish, and runs Distribution's collector directly against the shared volume. Untagged manifests and referenced multi-platform image data are preserved; the app does not pass `--delete-untagged`. In-progress pushes may need retrying. Pending upload directories are handled separately by Distribution's upload purger.

This maintenance workflow requires the provided **single-app** Compose topology: all registry clients must go through the Go service, and no other writer or app replica may access the volume during maintenance. The registry's blob-descriptor cache is disabled to prevent stale existence checks after cleanup. Neither service uses the Docker socket. Both run as UID 10001; the one-off `storage-init` service adjusts ownership of only the two application volumes before startup.

## Image deletion

The UI deletes **manifests by digest**, following the [Distribution API](https://distribution.github.io/distribution/spec/api/). Every tag referencing that manifest in the repository is removed; the confirmation lists the tags visible at the last refresh. A concurrently pushed alias to the same digest is also removed. Deleting a multi-platform index does not delete its child manifests. Empty repository names may remain in the registry catalog.

Deleting a manifest does not immediately reclaim blobs. Use Storage to reclaim unused data, or schedule downtime for manual maintenance:

```sh
docker compose stop app registry
docker compose run --rm --no-deps registry garbage-collect /etc/distribution/config.yml
docker compose up -d
```

Keep the app and registry stopped until manual collection finishes. Back up the Docker volumes before maintenance. See [Distribution garbage collection](https://distribution.github.io/distribution/about/garbage-collection/).

## Local development

Requires Go 1.26+ and Node 22+.

Start a development storage registry:

```sh
docker run -d --name magistry-dev-registry -p 127.0.0.1:5001:5000 \
  -e REGISTRY_STORAGE_DELETE_ENABLED=true \
  -e REGISTRY_HTTP_RELATIVEURLS=true \
  -v magistry-dev-data:/var/lib/registry registry:3
```

Run the Go API and React dev server in separate terminals:

```sh
go run ./cmd/magistry
```

```sh
cd web
npm ci
npm run dev
```

Use the printed Vite URL for the UI and `localhost:5050` for Docker commands. Vite proxies `/api` and `/v2` to Go. For a single-process development run, build React with `cd web && npm run build`, then run Go from the project root and visit port 5050.

Local account and settings files live in `.magistry/` (ignored by Git). For local storage reporting, set `REGISTRY_DATA_DIR` to the same directory used by Distribution. Cleanup also requires `REGISTRY_BINARY` and `REGISTRY_CONFIG`; the Docker image configures these automatically. Do not point local cleanup at a registry that also has directly connected clients.

### Environment

| Variable | Go default | Purpose |
| --- | --- | --- |
| `LISTEN_ADDR` | `127.0.0.1:5050` | Go HTTP listen address; Docker image uses `:5000` |
| `REGISTRY_URL` | `http://127.0.0.1:5001` | Internal Distribution origin |
| `WEB_DIR` | `web/dist` | Built frontend files |
| `PUBLIC_REGISTRY` | Browser host | Address used in UI commands; Compose defaults to `localhost:5050` |
| `AUTH_USERNAME`, `AUTH_PASSWORD` | Empty | Optional environment-managed administrator override |
| `DATA_DIR` | `.magistry` | Persistent account and settings files; Docker uses `/var/lib/magistry` |
| `REGISTRY_DATA_DIR` | Empty | Shared registry filesystem root; Docker uses `/var/lib/registry` |
| `REGISTRY_BINARY`, `REGISTRY_CONFIG` | Empty | Collector executable and config; preconfigured in Docker |
| `COOKIE_SECURE` | `false` | Set `true` behind HTTPS to require secure session cookies |
| `BIND_ADDRESS`, `PORT` | `127.0.0.1`, `5050` | Compose host binding |

### Checks

```sh
go test -race ./...
go vet ./...
cd web && npm ci && npm run build
docker compose config --quiet
```

For an integration check against a **disposable** running registry, use `python3 scripts/smoke.py http://127.0.0.1:5051` (replace the port as needed), with `SMOKE_USERNAME` and `SMOKE_PASSWORD` set. The test uploads an OCI image with two tags, verifies metadata and contents, then deletes the generated manifest. It may leave an empty repository in the catalog. On a new test volume, `SMOKE_BOOTSTRAP=1` also creates a test administrator. `SMOKE_STORAGE=1` tests soft limits, dry-run analysis, real garbage collection, and retention of a referenced image; use this only on disposable test data.

### Management API

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/healthz` | App liveness, without authentication |
| GET | `/api/auth/session` | Public setup state and current browser session |
| POST | `/api/auth/setup` | Create the first administrator and a session |
| POST | `/api/auth/login` | Sign in with administrator credentials |
| POST | `/api/auth/logout` | Revoke the browser session |
| GET | `/api/storage` | Volume usage and most recent cleanup job |
| PUT | `/api/storage/limit` | Persist a soft limit in bytes; 0 is unlimited |
| POST | `/api/storage/cleanup` | Start analysis (`dryRun: true`) or confirmed cleanup (`dryRun: false, confirm: true`) |
| GET | `/api/status` | Registry connectivity and public settings |
| GET | `/api/repositories` | Paginated upstream catalog, collected into a sorted array |
| GET | `/api/tags?repository=team/app` | Tags and image metadata |
| DELETE | `/api/manifests?repository=team/app&digest=sha256:…` | Remove a manifest and its tag aliases |
| All | `/v2/…` | Streaming proxy to Distribution for Docker / OCI clients |

No database is required. The Go service uses only the standard library. Registry metadata is fetched on demand, with six concurrent tag readers per request; no background indexing or vulnerability scanning is included.
