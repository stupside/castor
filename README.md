
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

Service settings are grouped under `servers.api`, `servers.media`, and `servers.scraping`. For example, set `CASTOR_SERVERS__SCRAPING__BROWSER__CHROME_PATH` to choose Chrome. Each command rejects unknown keys in the sections it owns when it loads them. Top-level `sources` holds the CLI's title-to-page templates; top-level `cast` holds playback defaults that an API request may override.

Set `servers.media.ffmpeg.path` and `servers.media.ffmpeg.probe_path` when the media tools are installed outside `PATH`; they default to `ffmpeg` and `ffprobe`.

Upgrading from 1.x? Follow the [1.x → 2.0 migration guide](migrations/1.x-to-2.0.md) before reusing your configuration or Docker setup.

### Logs

Every log line of a cast carries `cast_id`, the same in all three services, so one cast can be filtered from the rest. Set `telemetry.endpoint` (or `CASTOR_TELEMETRY__ENDPOINT`) on a server to also export its logs over OTLP/HTTP, such as `http://localhost:4318`; unset, logs stay on stderr and in `castor.log`.

### Subtitles

Castor shows the captions a source publishes (an HLS stream's WebVTT renditions) in the language you pick, as a native caption track on Chromecast and DLNA. Roku does not support these source caption tracks yet. If the source has no usable caption in that language, playback continues without captions by default. Set `cast.subtitles.transcribe_if_missing: true` to generate captions with Whisper in that case. Generated captions use a native track where the device and output support one; otherwise they are burned into the served video. The source's captions take priority, so the two never show together. The appropriate default model downloads once to your cache when transcription is enabled.

With several subtitle languages, Whisper uses the selected audio language when it is among them. If the source does not declare that language, it tries the first preference. It skips transcription when the declared audio language is outside the subtitle preferences.

```yaml
cast:
  subtitles:
    languages: [fr, en]   # try source caption languages in order
    # transcribe_if_missing: true  # generate captions if the source has none
servers:
  media:
    whisper:
      # model_path: ""     # ggml-tiny.en for en, multilingual ggml-tiny otherwise
```

Set `servers.media.whisper.model_path` to use a larger model (e.g. multilingual `ggml-base.bin`). English-only models reject other languages and `auto`.
The short form `subtitles: en` selects one source caption language.

### Audio language

```yaml
cast:
  audio:
    languages: [fr, en]   # try in order; unset plays the source's default track
```

Castor tries these languages across HLS variants, DASH audio representations, and audio tracks in direct files. If none matches, it uses the source's marked default track when one is known, otherwise the first track. A direct file with a selected audio track is served by Castor so the device cannot choose a different track.

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

Run on the server with `servers.media.token` set:

```sh
castor media-server   # listens on :8410 (servers.media.listen)
```

On your computer:

```yaml
servers:
  media:
    url: http://my-nas:8410
    token: "<a long random string, the same on both>"
```

Your computer discovers and controls the TV; the server processes media. Closing the terminal leaves playback running; Ctrl+C stops it. The API server applies top-level `cast` defaults, which a cast request can override; the Whisper model is configured under `servers.media`. Set `servers.media.advertise` to an address the TV can reach; use HTTPS outside a trusted network.

### The API, for apps and integrations

Run each command in a separate process; API must reach the TV's network:

```sh
castor media-server      # :8410
castor scraping-server   # :8412
castor api-server        # :8411
```

Set `servers.media.url: http://localhost:8410` and `servers.scraping.url: http://localhost:8412` for API. Configure independent `servers.media.token`, `servers.scraping.token`, and `servers.api.token`; `$TOKEN` below is the API token.

```sh
curl -X POST http://localhost:8411/castor.v1.DeviceService/ListDevices \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}'

curl -X POST http://localhost:8411/castor.v1.CastService/Cast \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"target": {"deviceId": "<id from ListDevices>"}, "source": {"stream": {"url": "https://example.com/video.m3u8"}}}'
```

For a page, use `"source": {"pages": {"urls": ["https://example.com/watch"]}}`. `Stop` ends a cast; `Watch` streams feedback (use [`buf curl`](https://buf.build/docs/reference/cli/buf/curl) or a generated [client](proto/castor/v1)). CLI clients connect with `servers.api.url` and `servers.api.token`.

Page casts go API → scraping → media; direct streams bypass scraping. Media never receives webpages. Without `servers.api.url`, CLI starts missing services on loopback. See [ARCHITECTURE.md](ARCHITECTURE.md) for contracts, ownership, and lifecycle.

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
   servers:
     api:
       devices:
         roku:
           password: "<dev-web-server-password>"   # first cast only
   ```
3. **Cast.** Castor sideloads its channel automatically; later casts reuse it.

For a published channel, set `servers.api.devices.roku.app_id` instead; no developer mode/password needed. Relayed Roku playback runs about 30 s behind.

## Network and firewall

- **Devices reach castor on one TCP port**: `servers.media.listen` of `castor media-server` (8410). Published media and captions are served directly through that port. Plain `castor` embeds a media server on a random LAN port each run, so allow the `castor` binary, or run `castor media-server` for a fixed port. `servers.media.advertise` and `servers.media.network.interface` pick the address devices are given.
- **The API (8411) and scraping (8412) servers are not for devices.** They bind every interface, so set their tokens (see [SECURITY.md](SECURITY.md)).
- **Discovery** sends SSDP (DLNA, Roku) to UDP 239.255.255.250:1900 and mDNS (Chromecast) to UDP 5353, and expects replies back, so it stays on one subnet unless you pin `host`.
- **Outbound control**: Chromecast TCP 8009, Roku TCP 8060 (80 for the first sideload), DLNA the port the device advertises. Castor opens no callback port.

## Troubleshooting

### Two devices share a name

Castor refuses to guess. `castor scan` prints each device's id as its last column; set it to pick one:

```yaml
device:
  type: dlna
  id: "uuid:..."           # a DLNA or Chromecast id can fall back to the address, which DHCP may change
```

### `castor scan` finds nothing: pin the device by IP

Discovery cannot cross VLANs/subnets and is blocked on Android/Termux. Set `host` to skip discovery:

```yaml
device:
  name: "Living Room TV"   # now just a label
  type: dlna
  host: 192.168.0.3        # the device's LAN IP
```

For DLNA, `host` can be a full description URL (e.g. `http://192.168.0.3:9197/dmr`). On Android/Termux, leave `servers.media.network.interface` empty.

### The page won't play

The page's video must start without a click. DRM streams are refused.

### The device loads the stream but plays nothing

Force served playback rather than handing the TV a link:

```yaml
cast:
  delivery: serve   # "auto" (the default) decides per source
```

Try once with `CASTOR_CAST__DELIVERY=serve`; it costs bandwidth and CPU. `castor --debug` explains delivery decisions; with servers running apart, their lines are in their own logs, filtered by `cast_id`.

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

The cache volume preserves Whisper models. On Linux, put `CASTOR_SERVERS__MEDIA__TOKEN`, `CASTOR_SERVERS__SCRAPING__TOKEN`, and `CASTOR_SERVERS__API__TOKEN` in git-ignored `.env`, then run `docker compose up -d`. API and media use host networking; scraping is published only on loopback. Remove the `/dev/dri` mapping on hosts without Intel VA-API. A standalone bridged media container needs `-p 8410:8410` and `servers.media.advertise` set to a device-reachable address.

Tags: `:latest` (stable), `:canary` (preview), or a pinned `:vX.Y.Z`.

## Purpose and disclaimer

No bundled video or sources; no DRM decryption or circumvention. You are responsible for lawful use and compliance with site terms. Provided as-is for lawful, personal, and educational use.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), and [ARCHITECTURE.md](ARCHITECTURE.md) for how castor works inside.
