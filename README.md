
<p align="center">
  <img src=".github/images/castor.svg" alt="Castor" width="200"/>
</p>

<p align="center">
  <a href="https://trendshift.io/repositories/86848?utm_source=trendshift-badge&amp;utm_medium=badge&amp;utm_campaign=badge-trendshift-86848" target="_blank" rel="noopener noreferrer"><img src="https://trendshift.io/api/badge/trendshift/repositories/86848/daily?language=Go" alt="stupside%2Fcastor | Trendshift" width="250" height="55"/></a>
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

Cast web video to your TV without screen mirroring. Castor extracts streams, converts incompatible formats, and optionally generates subtitles. Run locally or offload media processing to a NAS.

Castor bundles no content or sources and refuses DRM. Use only content you are authorized to access; see [Purpose and disclaimer](#purpose-and-disclaimer).

<p align="center">
  <img src=".github/images/screen-selection.png" alt="Browsing titles in the castor TUI" width="640"/>
  <br/>
  <sub><em>Run <code>castor cast</code> to browse titles and cast, without leaving the terminal.</em></sub>
</p>

## Quick start

Install on macOS ([other platforms](#installation)) and find your TV:

```sh
brew install --cask stupside/tap/castor
castor scan
```

Save `config.yaml` in your working directory:

```yaml
device:
  name: "Living Room TV"   # exact name from `castor scan`
  type: dlna               # or: chromecast, roku
```

Cast a page:

```sh
castor cast player https://example.com/watch/some-video
```

> `castor scan` found nothing? See [Troubleshooting](#troubleshooting).

## Commands

| Command | What it does |
| --- | --- |
| `castor scan` | List the devices on your network |
| `castor cast player <url>` | Cast a web page with an embedded video player |
| `castor cast url <url>` | Cast a direct stream or video URL |
| `castor cast` | Browse titles and cast, interactively (needs a [TMDB key](#tmdb-key)) |
| `castor cast movie <id>` | Resolve a movie id against your [sources](#sources) and cast |
| `castor cast episode <id> --season N --episode N` | Same, for a TV episode |
| `castor media-server` | Probe, rank, convert, and serve streams |
| `castor scraping-server` | Extract stream candidates using Chrome |
| `castor api-server` | Discover/control devices and orchestrate casts |

`castor cast --dry-run ...` prints the streams it found instead of casting. Run `castor --help` for all flags.

## Installation

Use a native binary on your TV's network, with these tools on `PATH`:

| Tool | Version | Used for |
| --- | --- | --- |
| **Chrome / Chromium** | Any recent | Finding the video on a page |
| **ffmpeg** | 7.1+ | Converting the video for your TV |
| **ffprobe** | 7.1+ | Reading the video's format |

Encoding uses a working GPU encoder, falling back to software.

- **macOS:** `brew install --cask stupside/tap/castor`.
- **Linux:** download `castor_<version>_linux_amd64.tar.gz` (or `_arm64`) from the [latest release](https://github.com/stupside/castor/releases/latest) and put `castor` on your `PATH`.
- **Windows:** download the [release](https://github.com/stupside/castor/releases/latest) ZIP for your architecture and put `castor.exe` on `PATH`. Install tools with `winget install Gyan.FFmpeg` and `winget install Google.Chrome`. For SmartScreen, choose *More info → Run anyway*; allow firewall access on **private** networks for discovery.
- **From source:** see [CONTRIBUTING.md](CONTRIBUTING.md).

## Configuration

Castor reads `config.yaml` (or `--config <path>`), then overlays git-ignored `config.local.yaml`. Environment variables such as `CASTOR_CAST__MAX_HEIGHT=720` override both. CLI casting needs `device`; other keys have defaults. Keep secrets out of git; see [SECURITY.md](SECURITY.md).

### Subtitles

Subtitles are burned into served, read-once video (e.g. DLNA), not self-fetching Chromecast/Roku playback. The appropriate default model downloads once to your cache.

```yaml
cast:
  subtitles: en            # a language code, or auto to detect it; unset for none
whisper:
  # model_path: ""         # ggml-tiny.en for en, multilingual ggml-tiny otherwise
```

Set `whisper.model_path` to use a larger model (e.g. multilingual `ggml-base.bin`). English-only models reject other languages and `auto`.

### Video quality

Choose the tallest stream within this ceiling and scale known taller video down:

```yaml
cast:
  max_height: 2160         # default: 1080
```

### Sources

Title commands substitute IDs into your source templates, trying proxies in order. Add only sites you are authorized to use:

```yaml
sources:
  - proxies: ["https://your-source.example"]   # base URLs, tried in order
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

### TMDB key

Only the interactive browser needs a [TMDB key](https://www.themoviedb.org/settings/api):

```yaml
tmdb:
  api_key: "<KEY>"
```

## Run castor on another machine

### A shared media server

Run on the server with `server.token` set:

```sh
castor media-server   # listens on :8410 (server.listen)
```

On your computer:

```yaml
server:
  url: http://my-nas:8410
  token: "<a long random string, the same on both>"
```

Your computer discovers and controls the TV; the server processes media. Closing the terminal leaves playback running; Ctrl+C stops it. Cast preferences come from the client, the Whisper model from media. Set `server.advertise` to an address the TV can reach; use HTTPS outside a trusted network.

### The API, for apps and integrations

Run each command in a separate process; API must reach the TV's network:

```sh
castor media-server      # :8410
castor scraping-server   # :8412
castor api-server        # :8411
```

Set `server.url: http://localhost:8410` and `scraping.url: http://localhost:8412` for API. Configure independent `server.token`, `scraping.token`, and `api.token`; `$TOKEN` below is the API token.

```sh
curl -X POST http://localhost:8411/castor.v1.DeviceService/ListDevices \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}'

curl -X POST http://localhost:8411/castor.v1.CastService/Cast \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"target": {"deviceId": "<id from ListDevices>"}, "source": {"stream": {"url": "https://example.com/video.m3u8"}}}'
```

For a page, use `"source": {"pages": {"urls": ["https://example.com/watch"]}}`. `Stop` ends a cast; `Watch` streams feedback (use [`buf curl`](https://buf.build/docs/reference/cli/buf/curl) or a generated [client](proto/castor/v1)). CLI clients connect with `api.url` and `api.token`.

Page casts go API → scraping → media; direct streams bypass scraping. Media never receives webpages. Without `api.url`, CLI starts missing services on loopback. See [ARCHITECTURE.md](ARCHITECTURE.md) for contracts, ownership, and lifecycle.

## Supported devices

| Protocol | Works with | Status |
| --- | --- | --- |
| **DLNA / UPnP** (`MediaRenderer:1`) | Most smart TVs, and players like Kodi, VLC, and Plex | Tested on Samsung |
| **Chromecast** | Google Cast devices | Experimental, not yet tried on real hardware |
| **Roku** | Roku TVs and players, through a sideloaded channel | Experimental, not yet tried on real hardware |

### Roku setup

Castor sideloads a playback channel:

1. **Turn on Developer Mode** (once, by hand): on the remote press **Home x3, Up x2, Right, Left, Right, Left, Right**, enable developer mode, and set a **web-server password**. The device reboots.
2. **Put the password in your config** (out of git, see [Configuration](#configuration)):
   ```yaml
   devices:
     roku:
       password: "<dev-web-server-password>"   # first cast only
   ```
3. **Cast.** Castor sideloads its channel automatically; later casts reuse it.

For a published channel, set `devices.roku.app_id` instead; no developer mode/password needed. Relayed Roku playback runs about 30 s behind.

## Troubleshooting

### `castor scan` finds nothing: pin the device by IP

Discovery cannot cross VLANs/subnets and is blocked on Android/Termux. Set `host` to skip discovery:

```yaml
device:
  name: "Living Room TV"   # now just a label
  type: dlna
  host: 192.168.0.3        # the device's LAN IP
```

For DLNA, `host` can be a full description URL (e.g. `http://192.168.0.3:9197/dmr`). On Android/Termux, leave `network.interface` empty.

### The page won't play

The page's video must start without a click. DRM streams are refused.

### The device loads the stream but plays nothing

Force served playback rather than handing the TV a link:

```yaml
cast:
  delivery: serve   # "auto" (the default) decides per source
```

Try once with `CASTOR_CAST__DELIVERY=serve`; it costs bandwidth and CPU. `castor --debug` explains delivery decisions.

## Docker

The image includes Chrome, ffmpeg, and ffprobe. `--device /dev/dri` enables Intel VA-API; otherwise encoding uses software.

> [!WARNING]
> The Compose stack targets Linux host networking for LAN discovery and device-reachable media URLs. On macOS/Windows, use the native binary for discovery. Docker Desktop 4.34+ has opt-in [host networking](https://docs.docker.com/engine/network/drivers/host/), but cannot bind directly to the host's network interfaces.

```sh
docker run --rm --network host ghcr.io/stupside/castor:latest scan

docker run --rm --network host --device /dev/dri \
  -v "$PWD/config.yaml:/config.yaml" \
  -v castor-cache:/root/.cache \
  ghcr.io/stupside/castor:latest \
  cast player https://example.com/watch/some-video
```

The cache volume preserves Whisper models. On Linux, put `CASTOR_SERVER__TOKEN`, `CASTOR_SCRAPING__TOKEN`, and `CASTOR_API__TOKEN` in git-ignored `.env`, then run `docker compose up -d`. API and media use host networking; scraping is published only on loopback. Remove the `/dev/dri` mapping on hosts without Intel VA-API. A standalone bridged media container needs `-p 8410:8410` and `server.advertise` set to a device-reachable address.

Tags: `:latest` (stable), `:canary` (preview), or a pinned `:vX.Y.Z`.

## Purpose and disclaimer

No bundled video or sources; no DRM decryption or circumvention. You are responsible for lawful use and compliance with site terms. Provided as-is for lawful, personal, and educational use.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), and [ARCHITECTURE.md](ARCHITECTURE.md) for how castor works inside.
