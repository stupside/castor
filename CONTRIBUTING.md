# Contributing to Castor

Bug reports, tested device reports, and pull requests are all welcome.

## Building from source

The whisper bindings use cgo, so building requires a one-time cmake build of the linked library:

```sh
git submodule update --init --recursive   # first checkout only
make build                                # builds libwhisper.a (~1 min), then ./castor
```

To run without producing a binary, export the build environment once per shell and use plain Go tooling:

```sh
eval "$(make env)"
go run . cast browse --source vidsrc
go test ./...
go vet ./...
```

With [direnv](https://direnv.net) installed, the checked-in `.envrc` exports the environment automatically on `cd`, so plain `go build`, `go run .`, and `go test ./...` just work after `direnv allow`.

## Notes

- Castor uses bleeding-edge Go (`go 1.26`): use `slices`/`maps` packages, `min`/`max`/`clear` builtins, range-over-int, generics. No hand-rolled equivalents.
- Don't add compatibility shims or dead fallback paths.
- Comments only when the *why* is non-obvious.
