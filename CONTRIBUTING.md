# Contributing to Castor

Bug reports, device reports, and pull requests are welcome.

## Scope

Castor is a general-purpose caster. These are declined:

- Bundled or default source lists
- Adapters or scrapers for a specific streaming site
- Anything mainly for reaching content you have no right to, such as defeating DRM, paywalls, or geo-restrictions

Welcome: bug fixes, device support, transcoding and subtitle work, and extraction robustness.

## Building from source

On macOS or Linux, with Go 1.27+ and cmake. The whisper bindings use cgo, so the first build compiles the library:

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make build   # libwhisper.a (~1 min), then castor
```

`go install` won't work: the bindings need that locally built library. For plain Go tooling, export the build environment once per shell (the checked-in `.envrc` does it on `cd` with [direnv](https://direnv.net)):

```sh
eval "$(make env)"
go run ./cmd/castor scan
go test ./...
```

`go test ./...` includes the end-to-end suite, which builds the three binaries and casts real streams from fake sites to fake devices on each family's protocol. It needs ffmpeg, ffprobe and Chrome, runs in real time, and is not run in CI; `go test -short ./...` skips it.

## Running the stack with Tilt

[Tilt](https://tilt.dev) runs the whole dev loop natively, with live reload. You need Node 24+ and Yarn on top of the build requirements above, and the tokens in a `.env` file, as in `docker-compose.yml`.

```sh
tilt up   # then open the UI Tilt prints
```

It starts these resources, each rebuilt when its sources change:

| Resource | What it runs |
| --- | --- |
| `lib` | `make lib`, the whisper library, skipped once built |
| `media-server` | `castor media-server` on `:8410` |
| `api-server` | `castor api-server` on `:8411`, pointed at the media server |
| `install` | `yarn install` for the web app |
| `app-generate` | regenerates the TypeScript client from `proto/` (`yarn workspace castor-app generate`) |
| `app` | the Next.js app on `:3000`, which calls both servers through its `/rpc` proxy |

Open the app on `http://localhost:3000`. To try it from a phone, use your machine's LAN address: `next.config.ts` allows the usual private ranges in dev.

## The rules

[ARCHITECTURE.md](ARCHITECTURE.md) explains why each of these holds.

### Checked by CI

| Rule | Checked by |
| --- | --- |
| Every test passes, and none skips (the whisper transcriber test, which needs a large model, is the one exception) | `test` job, outside the end-to-end suite |
| `go vet`, `staticcheck -checks all` and `gofmt -s` report nothing | `lint` |
| No unreachable code (`deadcode -test`; `castorNativeLog`, called only from C, is the one exception) | `lint` |
| `go fix -diff` suggests nothing | `lint` |
| The API server builds without cgo | `lint` |
| The protos pass `buf lint` and `buf format`, stay compatible with the latest release (`buf breaking`), and the committed generated code matches them | `proto` |

Lint tools are pinned, so a new release never turns an unrelated push red.

Tests exercise behaviour, never the shape of the code (import graphs, which call site passes a value), and never a library. Mutation-test what you add: break the behaviour, see the test fail with a clear message, restore.

### Held in review

- No tier imports another, tests included; tiers share only the generated contracts.
- Anything that reaches into the devices' network belongs to the API server; content belongs to the UIs.
- A server's entry point is its only package outside its internal tree, and only a binary's main imports it; the TUI lives inside the castor binary.
- Consumers declare narrow ports; only a tier's entry point lists device families, source formats and other adapters.
- Settings are injected at construction; a config struct lives with the package that reads it.
- Inside a tier, a package is a feature, a vocabulary, or a tool several features run; a package one feature uses lives inside it.
- Name folders for what they do, not for a layer: one responsibility per package, one concept per file, one file per strategy.
- Contract rules live in the protos and only the server checks them; API layers use the generated types, with no mirror types.
- The public contract holds only what an integrator cannot do alone.
- An optional lookup returns `(T, bool)`, never a nil to remember to check.
- A comment is one short line, only for what the code can't say, or nothing.
- Use Go 1.27 idioms over hand-rolled equivalents, with no compatibility shims or dead fallback paths.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org): `type(scope)?: summary`, with `type` one of `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`. Mark a breaking change with `feat!:` or a `BREAKING CHANGE:` footer. The types drive the changelog and the version bump.

Enable the local check once:

```sh
make hooks   # points core.hooksPath at .githooks/
```

## Releases

[release-please](https://github.com/googleapis/release-please) keeps a release PR up to date from the commits on `main`. Merging it tags `vX.Y.Z` and publishes the `castor` archives, the Docker image (`:latest`, the full `castor`), and the Homebrew cask.

For a preview, run the **canary-release** workflow on any branch. It publishes `ghcr.io/stupside/castor:canary` and moves no stable pointer.
