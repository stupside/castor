# Contributing to Castor

Bug reports, tested device reports, and pull requests are all welcome.

## Scope

Castor is a general-purpose caster and a proof of concept. To keep it that way,
some contributions are out of scope and will be declined:

- Bundled or default source lists (Castor ships none by design)
- Adapters or scrapers targeting a specific streaming site
- Anything whose main purpose is to access content you have no right to, such as
  defeating DRM, paywalls, or geo-restrictions

Welcome: bug fixes, new device support, transcoding and subtitle improvements,
and general robustness of the extraction pipeline.

## Building from source

The whisper bindings use cgo, so building requires a one-time cmake build of the linked library:

```sh
git submodule update --init --recursive   # first checkout only
make build                                # builds libwhisper.a (~1 min), then ./castor
```

To run without producing a binary, export the build environment once per shell and use plain Go tooling:

```sh
eval "$(make env)"
go run . scan          # discover devices, a quick check that the build runs
go test ./...
go vet ./...
```

With [direnv](https://direnv.net) installed, the checked-in `.envrc` exports the environment automatically on `cd`, so plain `go build`, `go run .`, and `go test ./...` just work after `direnv allow`.

## Commit messages

Castor uses [Conventional Commits](https://www.conventionalcommits.org). Each subject is `type(scope)?: summary`, where `type` is one of `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`. Use `feat!:` (or a `BREAKING CHANGE:` footer) for a breaking change. These types drive the changelog and the next version bump, so they are not cosmetic.

Enable the local check once; it rejects non-conforming messages before they land, and needs nothing but git and bash (no tools to install):

```sh
make hooks   # points core.hooksPath at .githooks/
```

## Releases

Releases are automated with [release-please](https://github.com/googleapis/release-please). Merging Conventional Commits to `main` keeps a standing release PR updated with the next version and `CHANGELOG.md`; merging that PR tags `vX.Y.Z` and publishes the binaries, the Docker image (`:latest`), and the Homebrew cask.

For a bleeding-edge preview, run the **canary** workflow (Actions tab) on any branch. It publishes `ghcr.io/stupside/castor:canary` at `vX.Y.Z-canary.<sha>` and never moves a stable pointer.

## Notes

- Castor uses bleeding-edge Go (`go 1.26`): use `slices`/`maps` packages, `min`/`max`/`clear` builtins, range-over-int, generics. No hand-rolled equivalents.
- Don't add compatibility shims or dead fallback paths.
- Comments only when the *why* is non-obvious.
