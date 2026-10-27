# Contributing to Castor

## Scope

Welcome: bug/device reports, fixes, device support, transcoding, subtitles, and extraction robustness. No bundled sources, site-specific scrapers, or DRM/paywall/geo-restriction circumvention.

## Building from source

Requires Go 1.27+, cmake, and a C/C++ toolchain on macOS/Linux:

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make build   # libwhisper.a (~1 min), then castor
```

`go install` alone cannot build the native Whisper dependency. Export its build environment for Go tooling (`.envrc` also supports direnv):

```sh
eval "$(make env)"
go run ./cmd/castor scan
go test -short ./...
```

Omit `-short` for real-time end-to-end tests: fake sites/devices, real streams, embedded and three-process services. These require Chrome, ffmpeg, and ffprobe and are not run in CI.

## Running the stack with Tilt

[Tilt](https://tilt.dev) runs the native stack with live reload. Also install Node 24+ and Yarn; set all three service tokens in `.env` as in `docker-compose.yml`.

```sh
tilt up
```

Tilt builds Whisper, installs app dependencies, generates the TypeScript client, and starts media (`:8410`), API (`:8411`), scraping (`:8412`), and Next.js (`:3000`). Open `http://localhost:3000` or your machine's LAN address. The app calls only public API through `/rpc`.

## Observing a cast

Set `telemetry.endpoint` to an OTLP/HTTP collector's base URL to export logs, traces and metrics (castor adds `/v1/logs`, `/v1/traces` and `/v1/metrics`). Every service needs it, and trace context follows a cast across them. Exported records carry `castor.cast.id`, and castor's own names sit under `castor.`.

```sh
docker run --rm -p 3000:3000 -p 4318:4318 \
  -v "$PWD/docker/grafana:/otel-lgtm/grafana/conf/provisioning/dashboards:ro" \
  grafana/otel-lgtm   # Grafana on :3000 (admin/admin) with the Castor dashboard
CASTOR_TELEMETRY__ENDPOINT=http://localhost:4318 castor cast
```

## The rules

### Checked by CI

- Tests pass without skips; end-to-end tests are excluded.
- `go vet`, `staticcheck -checks all`, `gofmt -s`, and `go fix -diff` are clean.
- `deadcode -test` finds no unreachable code except C-called `castorNativeLog`.
- API and scraping build without cgo.
- Protos pass lint, formatting, and generated-code comparison. RPC contracts are not stable yet.
- The app passes typecheck, lint, functional regressions, generated-code comparison, and production build.

Lint tools are pinned. Test behaviour, not import graphs, code shape, or libraries. Mutation-test additions: break the behaviour, confirm a clear failure, then restore it.

### Held in review

Follow [ARCHITECTURE.md](ARCHITECTURE.md):

- Share contracts, not service implementations (including tests). Binary main wires service entry points; internals and TUI stay private.
- API owns devices, scraping owns extraction, media owns playback, clients own content/UI. Consumers declare narrow ports; entry points wire adapters.
- Inject settings at construction; keep config with its consumer. Name packages by responsibility, nesting single-feature helpers; one concept per file and one file per strategy.
- Put contract rules in protos; validate requests and peer responses at RPC edges. Use generated types, not mirrors; expose only what integrators need.
- Optional lookups return `(T, bool)`. Comments explain only what code cannot, in one short line. Prefer Go 1.27 idioms; no compatibility shims or dead fallbacks.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org): `type(scope)?: summary`. Use `!` or a `BREAKING CHANGE:` footer for breaking changes. Enable local checking:

```sh
make hooks
```

## Releases

[release-please](https://github.com/googleapis/release-please) maintains the release PR from `main`. Merging tags `vX.Y.Z` and publishes archives, the `:latest` image, and the Homebrew cask. Run **canary-release** on any branch to publish `:canary` without changing stable releases.
