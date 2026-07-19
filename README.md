
<p align="center">
  <img src=".github/images/castor.svg" alt="Castor" width="200"/>
</p>

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
    <img src="https://img.shields.io/github/actions/workflow/status/stupside/castor/continuous-integration.yml?style=flat-square" alt="Build Status">
  </a>
</p>

# Castor

Imagine buying a very nice TV and figuring out it doesn't allow casting from random websites...

- Then you switch to the longest **HDMI cable** you can find.
- Then you start doing **screen mirroring**: your computer lags, resolution tanks, nothing feels right.

Castor is a CLI that extracts video streams from websites, handles format compatibility, and casts to your TV in real time, with optional auto-generated subtitles burned directly into the video.

<p align="center">
  <img src=".github/images/screen-selection.png" alt="Browsing TMDB titles in the castor TUI" width="640"/>
  <br/>
  <sub><em>Run <code>castor cast</code> to browse trending titles, search TMDB, inspect posters and metadata, then cast — without leaving the terminal.</em></sub>
</p>

> [!NOTE]
> **How does extraction work?**
>
> Castor launches a headless Chrome with a randomized browser fingerprint and stealth scripts to hide automation. It listens to all network activity via Chrome DevTools Protocol, captures video streams, then runs an action pipeline: click the page, navigate into the largest iframe, solve Cloudflare Turnstile if detected, and click again as fallback.
>
> This works on most streaming websites but won't beat sophisticated bot protection.


## Installation

Homebrew and source build a **native binary** that needs **Chrome/Chromium** (headless extraction), **ffmpeg** (transcoding), and **ffprobe** (format detection) on your `PATH`. The **Docker** image bundles all three, so there you only supply a config.

### Homebrew (macOS)

```sh
brew install --cask stupside/tap/castor
```

### Docker

`ghcr.io/stupside/castor` ships with Chrome, ffmpeg and ffprobe baked in.

```sh
# Discover devices (no config required)
docker run --rm --network host ghcr.io/stupside/castor:latest scan

# Cast a movie by id, mounting config.yaml and a persistent model cache
docker run --rm --network host \
  -v "$PWD/config.yaml:/config.yaml" \
  -v castor-cache:/root/.cache \
  ghcr.io/stupside/castor:latest \
  cast movie tt12300742
```

The `-v "$PWD/config.yaml:/config.yaml"` mount is what makes this work: Castor reads your device and sources from [`config.yaml`](config.yaml), which the container looks for at `/config.yaml`. `cast movie tt12300742` builds the player URL from the `sources` proxies in that file — no URL on the command line — so run every command from the directory holding your `config.yaml`.

> [!WARNING]
> **Streaming sites are volatile.** `cast movie` resolves the id against the `sources` proxies in your [`config.yaml`](config.yaml) — here `https://1embed.cc`. Those point at third-party streaming sites that can go offline, change domains, or start blocking at any time. When one stops resolving, rotate it: swap in a working mirror/domain in the `sources` proxies list in your [`config.yaml`](config.yaml).

> [!IMPORTANT]
> `--network host` is required: device discovery is SSDP multicast and the TV streams back from Castor's replay server — neither survives Docker's bridge network. Host networking is only real on **Linux**; on Docker Desktop (macOS/Windows) it won't reach your TV, so run the binary natively there instead.

The `castor-cache` volume keeps the auto-downloaded whisper models (~75 MB) between runs. Swap `:v1.4.0` for `:latest` to track the newest build, or for any other tag to pin a release.

### From source

Needs Go 1.26+ and cmake (the whisper.cpp bindings are cgo and link a locally built `libwhisper.a`):

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make          # builds libwhisper.a, then the castor binary
```

`go install` won't work: the vendored whisper.cpp bindings come in through a local `replace` and need that prebuilt static lib.


## Supported devices

### DLNA / UPnP

Any TV implementing the DLNA/UPnP `MediaRenderer:1` profile works, which covers virtually every smart TV sold in the last decade: **Samsung** (tested), **LG**, **Sony Bravia**, **Panasonic Viera**, **Philips**, **Hisense**, **TCL**, **VIZIO**, **Sharp**. Networked players like Kodi, VLC, and Plex also work.

Run `castor scan` to discover devices on your network.

### Chromecast

> [!WARNING]
> Experimental: implemented but untested. Contributions welcome.


## Quick start

Castor **requires a `config.yaml`** in the current directory (or pass `--config`). Everything mechanical ships with working defaults, so the file only has to say **which device to cast to**, **which sources to cast from**, and a free [TMDB API key](https://www.themoviedb.org/settings/api) for the browser.

```sh
# 1. Find your TV's exact name
castor scan
```

Create `config.yaml` with that name:

```yaml
device:
  name: "Living Room TV"   # exact name from `castor scan`
  type: dlna

sources:
  - proxies: ["https://vidsrc-embed.ru"]
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"

tmdb:
  api_key: "<YOUR_TMDB_API_KEY>"   # free from https://www.themoviedb.org/settings/api
```

```sh
# 2. Browse and cast from an interactive TUI
castor cast
```

`castor cast` first asks which device to cast to — every DLNA/UPnP renderer on your network, discovered on the fly and with your configured device pre-selected:

<p align="center">
  <img src=".github/images/screen-devices.png" alt="Selecting a cast target in the castor TUI" width="640"/>
</p>

Then it opens a TMDB-backed browser — filter by genre, search, inspect posters and metadata, drill into a series' episodes, and cast the one you pick.

> [!TIP]
> No TMDB key? You can skip the browser and cast a title straight from its id — `castor cast movie tt33028778` — using the sources in your config. See [Usage](#usage).


## Usage

```sh
# Interactive TMDB browser: search, pick a movie/episode, cast (needs tmdb.api_key)
castor cast

# Cast from a streaming site's player page
castor cast player https://1embed.cc/embed/movie/tt12300742

# Cast by IMDB/TMDB id, using the sources in your config
castor cast movie   tt33028778
castor cast episode tt2699128 --season 1 --episode 3

# Cast a raw stream URL directly
castor cast url https://example.com/stream.m3u8

# Useful flags
castor cast movie --dry-run tt33028778   # print found URLs without casting
castor --debug cast player https://...   # verbose logging
castor scan                              # discover devices on the network
castor info                              # version / build info
```


## Configuration

[Quick start](#quick-start) covers the required keys. Beyond those, everything mechanical (timeouts, probing, capture, transcoding, Chrome discovery) ships with working defaults. Override any of it in `config.yaml`, point at a different file with `--config`, drop secrets like your TMDB key into a git-ignored sibling `config.local.yaml` (it overlays `config.yaml`), or set `CASTOR_SECTION__FIELD` environment variables.

The one opt-in worth calling out is auto-generated subtitles, burned into the video:

```yaml
whisper:
  enable: true             # off by default
  # language: "fr"         # default: English
  # model_path: ""         # default: ggml-tiny.en (~75 MB, auto-downloaded)
```


## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
