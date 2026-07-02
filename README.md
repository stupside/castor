
<p align="center">
  <a href="https://github.com/stupside/castor/releases/latest">
    <img src="https://img.shields.io/github/v/release/stupside/castor?style=flat-square" alt="Latest Release">
  </a>
  <a href="https://pkg.go.dev/github.com/stupside/castor">
    <img src="https://img.shields.io/badge/Go-Reference-00ADD8?style=flat-square&logo=go" alt="Go Reference">
  </a>
  <a href="https://github.com/stupside/homebrew-tap/blob/main/Casks/castor.rb">
    <img src="https://img.shields.io/badge/Homebrew-Available-FBB040?style=flat-square&logo=homebrew" alt="Homebrew">
  </a>
  <a href="https://github.com/stupside/castor/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/stupside/castor?style=flat-square" alt="License">
  </a>
  <a href="https://github.com/stupside/castor/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/stupside/castor/ci.yml?style=flat-square" alt="Build Status">
  </a>
</p>

# Castor

Imagine buying a very nice TV and figuring out it doesn't allow casting from random websites...

- Then you switch to the longest **HDMI cable** you can find.
- Then you start doing **screen mirroring** but it makes everything too laggy.

In both cases I can't watch movies while coding, and that's a dealbreaker.

Castor is a CLI that extracts video streams from websites, handles format compatibility, and casts to your TV (DLNA, Chromecast) in real time.

> [!NOTE]
> **How does extraction work?**
>
> Castor launches a headless Chrome with a **randomized browser fingerprint** and stealth scripts to hide automation (spoofed `navigator.webdriver`, canvas/audio noise, fake plugins, etc.).
>
> It then **listens to all network activity** via Chrome DevTools Protocol, capturing requests that match video MIME types or streaming patterns.
>
> While listening, it runs a simple **action pipeline**: click the page, navigate into the largest iframe, solve Cloudflare Turnstile if detected, and click again as fallback.
>
> This works on most streaming websites but won't beat sophisticated bot protection, so **Castor might not resolve sources for every website.**

---

## Requirements

| Dependency | Purpose |
|---|---|
| **Chrome / Chromium** | Headless stream extraction |
| **ffmpeg** | Transcoding |
| **ffprobe** | Format detection |

---

## Usage

### Discover devices on your network

```sh
castor scan
```

Lists all available casting devices (name, type, address).

Once you find the device you want to cast to, update the `device.name` and `device.type` properties in your `config.yaml`.

> [!WARNING]
> DLNA has been tested but Chromecast support is experimental and might not work due to lack of a device. Contributions are more than welcome.

### Cast a video URL

Castor will **try** to resolve the stream from the URL (picking the best HLS variant if applicable), transcode if needed, and stream to the device in real time.

```sh
castor cast url https://example.com/stream.m3u8
```

### Cast a movie or episode using a source

Uses a configured source to find and cast content by its IMDB ID.

```sh
# Cast Primate from IMDB ID (https://www.imdb.com/title/tt33028778)
castor cast movie --source vidsrc tt33028778

# Cast The Leftovers S01E03 from IMDB ID (https://www.imdb.com/title/tt2699128)
castor cast episode --source vidsrc tt2699128 --season 1 --episode 3
```

### Cast from a direct player URL

Instead of adding sources to the config, you can reference the streaming website's player URL directly.

```sh
# Cast Primate from fmovies's video player.
castor cast player https://www.fmovies.gd/watch/movie/1315303
```

### Dry run

Add `--dry-run` to any cast subcommand to print the resolved streaming URLs instead of casting:

```sh
castor cast url --dry-run https://example.com/stream.m3u8
```

### Subtitles

Castor can generate subtitles locally via
[whisper.cpp](https://github.com/ggml-org/whisper.cpp) by setting
`whisper.enable: true` in `config.yaml` (or `CASTOR_WHISPER__ENABLE=true`).
There is no CLI flag — config is the only toggle. The whisper.cpp engine is
linked statically into the castor binary; no separate `whisper-cli` install
is required. The default `ggml-tiny.en` model (~75MB) and the Silero VAD
model auto-download once to your user cache the first time it runs.

Subtitles are burned into the video (TVs cannot be trusted to render
DLNA-delivered caption tracks, sidecar or in-band). The pipeline reads the
source exactly once: a single puller downloads the stream into a local
spool while teeing the audio to whisper, and the realtime-paced encoder
follows behind, stamping the live transcript onto the frames. Transcription
streams with the LocalAgreement-2 policy — words are committed once two
consecutive passes agree on them — gated by Silero VAD so silence and music
never reach the model. Playback starts as soon as whisper has a ~20-second
head start — a few seconds of wall time — and the transcript keeps pulling
ahead of the encoder for the rest of the film. Override
`whisper.model_path` / `whisper.language` in `config.yaml` to customize.

#### Building from source

Because the whisper bindings are cgo, building castor requires a one-time
cmake build of the linked library:

```sh
git submodule update --init --recursive   # first checkout only
make build                                # cmake-builds libwhisper.a once (~1 min), then produces ./castor

# Run without producing a binary first:
make run ARGS="cast browse --source vidsrc"

# Or, for plain `go run .` / `go build`, export the env once per shell:
eval "$(make env)"
go run . cast browse --source vidsrc
```

With [direnv](https://direnv.net) installed, the checked-in `.envrc` exports
that environment automatically on `cd` — plain `go build` / `go run .` just
work (`direnv allow` once).

### Debug mode

Add `--debug` to enable verbose logging, useful for troubleshooting extraction or casting issues:

```sh
castor --debug cast movie --source example tt33028778
```

### Build info

```sh
castor info
```

Prints version, commit, and build time.

---

## Configuration

Castor uses a YAML config file (`config.yaml` by default, override with `--config`).
A sibling `config.local.yaml`, if present, overlays it with personal values
(API keys, device names) and is git-ignored — put secrets there, never in
`config.yaml`.
Everything except the device and the sources has a sane default, so a minimal
config is just:

```yaml
device:
  name: "Living Room TV" # castor scan
  type: dlna

sources:
  - name: vidsrc
    proxies: ["https://vidsrc-embed.ru"]
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

`castor scan` works without any config at all. The network interface for the
local stream server is auto-detected from the default route; set
`network.interface` to pin one.

---

## Contributing

Castor might be unstable but works for simple use cases. If you want to help make it better, feel free to open a PR.

- [x] Subtitles support (whisper.cpp auto-generation, burned in live)
- [ ] Devtool record plugins
- [ ] Play from a specific position
- [ ] Seek actions on the TV (really needed)
- [ ] Chromecast support (couldn't test and implement it properly due to lack of a device)
