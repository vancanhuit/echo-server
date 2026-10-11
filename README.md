# Echo Server

A Go 1.27.2 port of [Ealenn/Echo-Server](https://github.com/Ealenn/Echo-Server),
using [Echo v5](https://echo.labstack.com/docs). It echoes requests on any path
and method and lets callers control status codes, headers, bodies, and latency.

## Run

Install [mise](https://mise.jdx.dev/getting-started.html), then:

```sh
mise trust
mise install
mise run setup
cp .env.example .env
mise run dev
```

```sh
curl http://localhost:8080/hello?name=world
curl -X POST http://localhost:8080/example \
  -H 'Content-Type: application/json' -d '{"hello":"world"}'
curl 'http://localhost:8080/?echo_code=201&echo_body={"ok":true}' --globoff
curl http://localhost:8080/openapi.yaml
```

Open **[Swagger UI](http://localhost:8080/swagger/)** to browse the OpenAPI 3.1
documentation and send requests using **Try it out**. `/swagger` redirects to
`/swagger/`. The UI targets the server's current origin, including custom ports.

The example environment disables environment and filesystem exposure. Without
those overrides, both features are **enabled**, matching upstream: callers can
read process environment values and any file accessible to the server user.
Request logs can also include headers, cookies, bodies, and environment values.
Use an isolated environment for diagnostics with these features enabled.

## Response controls

| Query | Header | Effect |
| --- | --- | --- |
| `echo_code` | `X-ECHO-CODE` | Status in 200–599, or random choice from `200-404-500` |
| `echo_body` | `X-ECHO-BODY` | JSON value if valid JSON, otherwise a JSON string |
| `echo_env_body` | `X-ECHO-ENV-BODY` | Value of the named environment variable |
| `echo_header` | `X-ECHO-HEADER` | Headers such as `One:1, Two:2` (comma-space separator) |
| `echo_time` | `X-ECHO-TIME` | Delay in milliseconds; default accepted range 0–60000 |
| `echo_file` | `X-ECHO-FILE` | File contents or sorted directory entries |

Status/header query controls are applied after their header equivalents.
Body/environment/file controls prefer a nonempty header. Delays use the first
valid nonzero value, header before query. Invalid status candidates and delays
are ignored. Valid status candidates appear in `X-ECHO-RANDOM-STATUS`.

Response body priority: file → environment variable → custom body → echo.
Regular files force status 200; directories retain the selected status. Missing
files return 404. A missing environment variable returns an empty body.
`HEAD`, 204, 205, and 304 responses have no body.

Default response sections are `host`, `http`, `request`, and `environment`.
The request section includes path params, query, cookies, parsed body, and
lowercase headers. Forwarded headers are echoed but not trusted for IP/protocol.
`/ping` is an ordinary echo route and can be used for health checks.

## Configuration

Runtime configuration is environment-based, with upstream-style colon-separated
flags overriding environment values. Boolean flags use `=false` or `=true`.
Environment names use a single underscore between configuration segments,
for example `LOGS_LEVEL` and `MIDDLEWARE_CORS_ORIGINS`.

```sh
mise exec -- go run ./cmd/echo-server --port=9090 --enable:cookies=false
```

[mise](https://mise.jdx.dev/environments/) supplies default `PORT=8080` and
`LOGS_LEVEL=info`, preserves existing shell values, and loads optional `.env`
overrides. The binary itself does not read dotenv files. Local mise overrides
can also go in ignored `mise.local.toml`.

| Environment variable | Binary default | CLI flag |
| --- | --- | --- |
| `PORT` | `80` | `--port` |
| `ENABLE_HOST` | `true` | `--enable:host` |
| `ENABLE_HTTP` | `true` | `--enable:http` |
| `ENABLE_REQUEST` | `true` | `--enable:request` |
| `ENABLE_COOKIES` | `true` | `--enable:cookies` |
| `ENABLE_HEADER` | `true` | `--enable:header` |
| `ENABLE_ENVIRONMENT` | `true` | `--enable:environment` |
| `ENABLE_FILE` | `true` | `--enable:file` |
| `CONTROLS_TIMES_MIN` | `0` | `--controls:times:min` |
| `CONTROLS_TIMES_MAX` | `60000` | `--controls:times:max` |
| `COMMANDS_HTTPCODE_HEADERRESPONSE` | `true` | `--commands:httpCode:headerResponse` |
| `LOGS_APP` | `echo-server` | `--logs:app` |
| `LOGS_LEVEL` | `debug` | `--logs:level` |
| `LOGS_FORMAT` | `default` | `--logs:format` |
| `LOGS_IGNORE_PING` | `false` | `--logs:ignore:ping` |

`ENABLE_HEADER` controls custom response headers, not echoed request headers.
Disabling cookies produces `cookies: []` and removes the echoed `cookie` header.
Disabling environment also disables `echo_env_body`.

Every command supports `COMMANDS_<NAME>_QUERY` and `COMMANDS_<NAME>_HEADER`:

| NAME | CLI name | Default query | Default header |
| --- | --- | --- | --- |
| `HTTPBODY` | `httpBody` | `echo_body` | `x-echo-body` |
| `HTTPENVBODY` | `httpEnvBody` | `echo_env_body` | `x-echo-env-body` |
| `HTTPCODE` | `httpCode` | `echo_code` | `x-echo-code` |
| `HTTPHEADERS` | `httpHeaders` | `echo_header` | `x-echo-header` |
| `TIME` | `time` | `echo_time` | `x-echo-time` |
| `FILE` | `file` | `echo_file` | `x-echo-file` |

For example, `COMMANDS_HTTPBODY_QUERY=body` or
`--commands:httpBody:query=body` renames `echo_body` to `body`.

Logs use Go `slog` JSON on stdout through Echo's `RequestLogger` middleware.
Every completed request includes a request ID, method, URI, final status, latency in
milliseconds, and response size in bytes. `default` includes a message and
available echo data, `line` includes the message, and `object` includes available
echo data. Accepted levels: `trace`, `debug`, `info`, `warn`, `error`, `fatal`.
Successful handler execution logs at `info` (including intentionally controlled
error statuses); returned errors and recovered panics log at `error`.
Echo's default centralized error handler returns JSON errors, hides internal
diagnostics, and leaves committed responses alone. `Recover` runs inside the
request logger so panics are included in the final request log.
SIGINT/SIGTERM initiate graceful shutdown with a 10-second deadline, using
Echo v5's [`StartConfig.Start`](https://echo.labstack.com/cookbook/graceful-shutdown/).

### Optional middleware

Echo's [Request ID](https://echo.labstack.com/middleware/request-id/) middleware
always supplies `X-Request-ID`, preserving an incoming ID or generating one.
The same ID appears as `request_id` in JSON logs, including failed requests.
IDs are diagnostic labels supplied by clients, not authentication identities.

The following settings are opt-in and support the same environment/CLI
precedence as the other configuration. Commented examples are in `.env.example`.

| Environment variable | Default | CLI flag |
| --- | --- | --- |
| `MIDDLEWARE_GZIP` | `false` | `--middleware:gzip` |
| `MIDDLEWARE_SECURE` | `false` | `--middleware:secure` |
| `MIDDLEWARE_TIMEOUT` | `0` (disabled) | `--middleware:timeout` |
| `MIDDLEWARE_CORS_ORIGINS` | empty (disabled) | `--middleware:cors:origins` |
| `MIDDLEWARE_RATELIMIT_RATE` | `0` (disabled) | `--middleware:rateLimit:rate` |
| `MIDDLEWARE_RATELIMIT_BURST` | `1` | `--middleware:rateLimit:burst` |

- [Gzip](https://echo.labstack.com/middleware/gzip/) compresses responses for
  clients requesting gzip. Bodyless statuses remain bodyless; centralized
  error responses are sent uncompressed.
- [Secure](https://echo.labstack.com/middleware/secure/) adds Echo's default
  `X-Content-Type-Options`, `X-Frame-Options`, and `X-XSS-Protection` headers.
  HSTS is not enabled. Custom echo response-header controls can override
  middleware-supplied headers, including security and CORS headers.
- [ContextTimeout](https://echo.labstack.com/middleware/context-timeout/)
  accepts Go durations such as `500ms`, `5s`, or `65s`. A timed-out echo delay
  returns **503 Service Unavailable**, using Echo's native deadline-error
  handling. A timeout shorter than `echo_time` intentionally interrupts that
  delay. Cancellation is cooperative: this is not a hard I/O deadline and
  does not forcibly interrupt filesystem operations or request-body reads.
  The HTTP server's separate 30-second read timeout still applies.
- [CORS](https://echo.labstack.com/middleware/cors/) takes comma-separated
  origins, such as `https://example.com,http://localhost:3000`. Allowed browser
  preflights return 204; ordinary OPTIONS requests still echo. Requested
  headers are allowed for configured origins; credentials are not enabled.
  Request-ID, random-status, and rate-limit response headers are exposed.
  `*` permits all origins only when explicitly configured.
- [RateLimiter](https://echo.labstack.com/middleware/rate-limiter/) uses a
  per-process, in-memory token bucket for each direct peer IP. Rate is requests
  per second; burst is a positive integer. Rejections return 429 and
  `Retry-After`. Forwarded IP headers cannot bypass the limit; clients behind
  the same reverse proxy share its bucket. CORS preflights bypass this quota.

Recovery and request logging wrap these options, so middleware failures are
logged with their final status. Multipart file uploads remain rejected (400);
multipart fields remain supported within the decoded 1 MiB request-body limit.

## OpenAPI and development

[`api/openapi.yaml`](api/openapi.yaml) is the OpenAPI 3.1.0 source, embedded in
the binary at `GET /openapi.yaml`. Models are generated with the pinned
`oapi-codegen` v2.8.0 tool and used by the handler. This release validates the
specification before generation and supports OpenAPI 3.1 natively. Routes are registered directly in
Echo v5 to preserve wildcard and arbitrary-method behavior. OpenAPI represents
the root and a path parameter; the runtime also accepts multi-segment paths.
The specification describes default command names and delay bounds.

Swagger UI v5 is served at `/swagger/` using
[`swgui/v5emb`](https://github.com/swaggest/swgui). Its JavaScript, CSS, and
icons are embedded in the Go binary, so the standalone binary and Distroless
image work without a CDN. Online specification validation is disabled. The
documentation routes use the server's request IDs, logging, and optional
security middleware; configured rate limits also apply to UI and asset requests.
Swagger's asset handler handles compression itself to avoid double gzip.

```sh
mise run generate        # regenerate models after spec changes
mise run fmt             # gofmt
mise run check           # race tests, lint, generated-code drift check
mise run vuln            # govulncheck
mise run build           # bin/echo-server
bin/echo-server --version
```

Tools are pinned in `mise.toml` and `mise.lock`. Bun manages the existing
commitlint/Husky development hooks; the server has no JavaScript runtime.

### Build metadata

Both `mise run build` and `mise run docker:build` inject:

- `VERSION`: `git describe --tags --always --dirty`, or `dev` outside Git.
- `COMMIT`: full Git commit SHA, or `unknown` outside Git.
- `BUILD_DATE`: current UTC time in RFC 3339 format.

Set these environment variables to override the defaults in CI:

```sh
VERSION=v1.1.0 COMMIT="$(git rev-parse HEAD)" BUILD_DATE=2026-10-11T00:00:00Z mise run build
```

Metadata appears in `--version` output and startup logs. Plain `go run`/`go build`
uses `dev`/`unknown` defaults unless linker flags are supplied.

Local, Docker, and GoReleaser builds use `-trimpath -buildvcs=false` to omit
local filesystem paths and automatic VCS stamping. Version, commit SHA, and
build date are still injected explicitly through linker flags.
The linker package's module path is discovered with `go list -m`; GoReleaser
uses its equivalent `{{ .ModulePath }}` template value.

### Docker

```sh
mise run docker:build
docker run --rm echo-server:local --version
docker run --rm -p 127.0.0.1:8080:8080 \
  -e ENABLE_ENVIRONMENT=false -e ENABLE_FILE=false echo-server:local
```

`docker:build` builds the native platform and loads `echo-server:local` for local
use. To build both Linux/AMD64 and Linux/ARM64 without publishing:

```sh
mise run docker:build:multi
```

This exports a multi-platform OCI archive to `dist/echo-server-image.tar`.
It requires Docker Buildx with a multi-platform-capable builder. Docker Engine
29+ with the containerd image store supports this directly. On older installations:

```sh
docker buildx create --name echo-server-builder --driver docker-container --bootstrap --use
```

The build stage uses `debian:13` with mise 2026.10.7 installing Go from the
project's pinned tool configuration. The builder runs on `$BUILDPLATFORM` and
cross-compiles with `TARGETOS`/`TARGETARCH`, so ARM64 builds do not require QEMU.
The runtime is Google's
`gcr.io/distroless/static-debian13:nonroot`, running the static binary as
UID/GID 65532.
Mise passes metadata as Docker build arguments; the image also records OCI
version, revision, and created labels. For direct Docker builds, pass them:

```sh
docker build -t echo-server:local \
  --build-arg VERSION=v1.1.0 \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" .
```

Without build arguments, direct Docker builds use `dev`/`unknown` metadata.

## CI/CD and releases

The repository is hosted at [vancanhuit/echo-server](https://github.com/vancanhuit/echo-server).
GitHub Actions uses pinned [`jdx/mise-action`](https://github.com/jdx/mise-action)
to install the tools from `mise.toml`/`mise.lock`, then runs the same tasks as local development.

- **CI:** pushes to `main`, pull requests, and manual runs execute `mise run ci`.
  This installs dependencies, runs race tests, lint, OpenAPI drift checks,
  workflow and GoReleaser validation, vulnerability checks, a binary build,
  cross-platform release snapshot builds, and the AMD64/ARM64 Distroless image build.
- **Release:** pushing a semantic-version tag such as `v1.1.0` runs
  `mise run release`: checks run first, then `mise run docker:publish` builds and
  pushes `ghcr.io/vancanhuit/echo-server:v1.1.0` as an AMD64/ARM64 manifest list.
  Only after image publication succeeds does
  [GoReleaser](https://goreleaser.com/customization/builds/go/) create the GitHub
  Release and upload its archives. Image build/push failures stop the task before
  a GitHub Release is created.
  The workflow uses GitHub's built-in token; no personal access token is required.

Release archives include static binaries for Linux, macOS, and Windows on
amd64 and arm64, the README, license, and OpenAPI specification. GitHub Releases
also include SHA-256 checksums and the standalone OpenAPI document. The `v`
prefix is retained in release names, binary versions, archive names, and
container tags. Prerelease tags such as `v1.2.0-rc.1` create GitHub prereleases.
Container releases include Linux/AMD64 and Linux/ARM64 under the same exact
version tag. Docker selects the matching architecture when pulling.

```sh
mise run ci                # complete local equivalent of CI
mise run release:check     # validate GoReleaser configuration
mise run release:snapshot  # build archives in dist/ without publishing
```

Choose each version according to SemVer: patch for fixes, minor for compatible
features, major for breaking changes. Version bumps are explicit Git tags;
commit messages do not automatically create releases.

To publish a tested commit on `main`:

```sh
git pull --ff-only
git tag -a v1.1.0 -m "Release v1.1.0"
git push origin v1.1.0
```

The release task requires a clean tagged checkout, `VERSION` matching the tag,
GHCR authentication, and `GITHUB_TOKEN` for GoReleaser. Never reuse an already
published version tag. To publish only the multi-arch container manually after
authenticating with `docker login ghcr.io`:

```sh
VERSION=v1.1.0 mise run docker:publish
```

## Compatibility notes

The port follows upstream commit
[`2b735482f942cbd889f1d49f3ff892364d0519ac`](https://github.com/Ealenn/Echo-Server/tree/2b735482f942cbd889f1d49f3ff892364d0519ac).
The HTTP controls, enable switches, command aliases, and response shape are
preserved, with these deliberate differences:

- Environment names use single underscores. Rename upstream double-underscore
  keys in `.env` files and deployments; the old names are not recognized.
- JSON uses Go's `encoding/json/v2`: duplicate names and invalid UTF-8 are
  rejected, and numeric request values retain their original precision.
- A uniform 1 MiB decoded request-body limit applies. JSON objects/arrays,
  JSON-LD, plain text, URL-encoded forms, and multipart fields are parsed for
  all methods; gzip/deflate are supported. Multipart uploads are rejected.
- Repeated query/form keys become arrays; Express `qs` bracket nesting is not
  implemented (e.g. `a[b]` stays a literal key).
- Malformed custom headers and overrides of HTTP framing headers return 400.
  Files are streamed as `text/plain`; special devices are not served.
- Log envelopes use `slog`, not Bunyan. Direct Seq transport is not ported;
  use a stdout log collector. Enabling `LOGS_SEQ_ENABLED` fails explicitly.
- Go-style boolean flag syntax requires `--flag=false`, not `--flag false`.
- Deployment packaging is the Dockerfile; upstream Helm charts are not copied.

## License and attribution

Ported from Ealenn/Echo-Server. The upstream license text is retained verbatim
in [`LICENSE.txt`](LICENSE.txt) (GNU GPL v3). The upstream README calls it LGPL,
but its actual license file contains GPL v3. This repository contains a new Go
implementation based on that project's behavior and tests.
