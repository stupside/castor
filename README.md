
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
  <a href="https://www.npmjs.com/package/@castor-dev/cli">
    <img src="https://img.shields.io/npm/v/@castor-dev/cli?style=flat-square&logo=npm" alt="npm">
  </a>
  <a href="https://github.com/stupside/castor/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/stupside/castor?style=flat-square" alt="License">
  </a>
  <a href="https://github.com/stupside/castor/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/stupside/castor/continuous-integration.yml?style=flat-square" alt="Build Status">
  </a>
</p>

# Castor

Cast web video to your TV without screen mirroring. Castor finds streams, converts incompatible formats, and optionally generates subtitles. Run locally or offload media processing to a NAS.

No bundled content or sources; DRM streams are refused. Use content you are authorized to access.

<p align="center">
  <img src=".github/images/screen-selection.png" alt="Browsing titles in the castor TUI" width="640"/>
</p>

## Quick start

Install with one of (requirements in [Installation](#installation)):

```sh
# macOS
brew install --cask stupside/tap/castor

# Any platform, Node 20+
npm install -g @castor-dev/cli

# From source, Go 1.27+ and cmake
git clone --recurse-submodules https://github.com/stupside/castor.git && cd castor && make build
```

Then discover your devices:

```sh
castor scan
```

Save `config.yaml` in your working directory using the name and type from the scan:

```yaml
device:
  name: "Living Room TV"
  type: dlna               # dlna, chromecast, or roku
```

Then cast a page or a direct video URL:

```sh
castor cast player https://example.com/watch/some-video
castor cast url https://example.com/video.m3u8
```

If discovery finds nothing, see [Troubleshooting](#troubleshooting).

## Installation

Whichever way you install, you also need Chrome/Chromium for webpage extraction and **ffmpeg + ffprobe 7.1+** for media processing, on `PATH`. Encoding uses a working GPU encoder when available, with software fallback.

On Windows: `winget install Gyan.FFmpeg` and `winget install Google.Chrome`, and allow Castor on private networks.

No Homebrew or Node? Extract the [latest release](https://github.com/stupside/castor/releases/latest) for your platform (`.tar.gz` for Linux, `.zip` for Windows) and put `castor` or `castor.exe` on `PATH`.

## Commands

| Command | Purpose |
| --- | --- |
| `castor scan` | Discover devices |
| `castor cast player <url>` | Cast a webpage's video |
| `castor cast url <url>` | Cast a direct stream or video |
| `castor cast` | Browse titles interactively; requires a TMDB key and sources |
| `castor cast movie <id>` | Cast a movie using your source templates |
| `castor cast episode <id> --season N --episode N` | Cast an episode using your source templates |

Add `--dry-run` to a cast command to list resolved streams without casting, and `--at 1h12m` (or `1:12`) to begin at a position; `castor cast` asks for one after you pick a title. Use `castor --help` for all flags and `--debug` for diagnostics.

## Configuration

Castor loads `config.yaml` (or `--config <path>`), overlays git-ignored `config.local.yaml`, then applies environment overrides such as `CASTOR_CAST__MAX_HEIGHT=720`. Keep secrets in the local file or environment. See the annotated [config.yaml](config.yaml) for service settings and the [migration guide](migrations/1.x-to-2.0.md) when upgrading from 1.x.

### Playback preferences

```yaml
cast:
  max_height: 1080          # select within this ceiling; scale taller video
  max_bitrate: 6M           # ask no more of the device's link; a heavier picture, and sound, are re-encoded under it
  audio:
    languages: [fr, en]    # ordered preferences; otherwise source default
  subtitles:
    languages: [fr, en]    # ordered source caption preferences
    transcribe_if_missing: false  # opt in to Whisper-generated captions
  delivery: auto           # serve forces playback through Castor
```

Source captions are supported on Chromecast and DLNA. Without usable captions, playback continues without subtitles unless transcription is enabled. Generated captions use a native track where supported, otherwise they are burned into video. Whisper downloads its default model once; set `servers.media.whisper.model_path` for a custom model. English-only models require English.

### Title browsing and sources

Interactive browsing needs a [TMDB key](https://www.themoviedb.org/settings/api). Title commands need templates for sites you are authorized to use; proxies are tried in order:

```yaml
tmdb:
  api_key: "<KEY>"
sources:
  - proxies: ["https://your-source.example"]
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

## Supported devices

| Protocol | Support |
| --- | --- |
| DLNA / UPnP (`MediaRenderer:1`) | Smart TVs and compatible players; tested on Samsung |
| Chromecast | Experimental; not yet tested on real hardware |
| Roku | Experimental; requires a playback channel; not yet tested on real hardware |

### Roku setup

1. Enable Developer Mode: press **Home ×3, Up ×2, Right, Left, Right, Left, Right**, then enable it and set a web-server password. The device reboots.
2. Set `servers.api.devices.roku.password` in `config.local.yaml`.
3. Castor sideloads its channel on the first cast and reuses it afterward.

For a published channel, set `servers.api.devices.roku.app_id` instead. Relayed Roku playback runs about 30 seconds behind.

## Remote services

By default, CLI commands embed missing services. To offload processing, run `castor media-server` on your NAS with `servers.media.token` set. On your computer:

```yaml
servers:
  media:
    url: http://my-nas:8410
    token: "<same token as the server>"
```

Your computer controls the TV; the NAS processes and serves media. Set `servers.media.advertise` on the server if its chosen address is unreachable from the TV.

For apps or a shared API, run each service separately:

| Command | Default port | Runs where |
| --- | --- | --- |
| `castor media-server` | 8410 | Devices can fetch its media URLs |
| `castor scraping-server` | 8412 | Chrome is installed |
| `castor api-server` | 8411 | It can discover and control devices |

Configure API with `servers.media.url`, `servers.scraping.url`, and their tokens. Give each service its own token; clients use `servers.api.url` and `servers.api.token`. Standalone servers listen on all interfaces by default. Use HTTPS outside a trusted network; see [SECURITY.md](SECURITY.md).

The API exposes device listing, cast/stop, and status watching through [Connect contracts](proto/castor/v1). For example, list devices:

```sh
curl -X POST http://localhost:8411/castor.v1.DeviceService/ListDevices \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}'
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for service ownership and cast lifecycle.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| No devices discovered | Discovery normally stays on one subnet. Set `device.host` to the TV's IP to skip it; for DLNA, a description URL also works. Required on Android/Termux. |
| Devices share a name | Set `device.id` to the ID printed by `castor scan`. |
| Page video is not found | The video must start without a click; DRM streams are refused. |
| TV loads but cannot play | Try `CASTOR_CAST__DELIVERY=serve castor --debug cast url <url>` to force served playback. |
| TV cannot fetch served media | Allow Castor through the firewall and check `servers.media.advertise` and `servers.media.network.interface`. Leave the interface unset on Android/Termux. |

Devices fetch media and captions on **8410** for a standalone media server, or a random LAN port for an embedded one. Discovery uses SSDP UDP 1900 (DLNA/Roku) and mDNS UDP 5353 (Chromecast). Outbound control uses Chromecast TCP 8009, Roku TCP 8060 (80 for sideloading), or DLNA's advertised port. API and scraping ports are for service clients.

Logs include a shared `cast_id` across services. With remote services, inspect each server's logs.

Set `telemetry.endpoint` to an OTLP/HTTP collector's base URL to export logs, traces and metrics (castor adds `/v1/logs`, `/v1/traces` and `/v1/metrics`). Every service needs it, and trace context follows a cast across them. Exported records carry `castor.cast.id`, and castor's own names sit under `castor.`.

```sh
docker run --rm -p 3000:3000 -p 4318:4318 \
  -v "$PWD/docker/grafana:/otel-lgtm/grafana/conf/provisioning/dashboards:ro" \
  grafana/otel-lgtm   # Grafana on :3000 (admin/admin) with the Castor dashboard
CASTOR_TELEMETRY__ENDPOINT=http://localhost:4318 castor cast
```

## Docker

The image includes Chrome, ffmpeg and ffprobe. Use **Linux host networking** for discovery and device-reachable media URLs; native binaries are simpler for discovery on macOS/Windows.

```sh
docker run --rm --network host \
  -v "$PWD/config.yaml:/config.yaml:ro" \
  -v castor-cache:/root/.cache \
  ghcr.io/stupside/castor:latest \
  cast player https://example.com/watch/some-video
```

Add `--device /dev/dri` for Intel VA-API; otherwise encoding uses software. The cache volume preserves Whisper models.

For the [Compose stack](docker-compose.yml), put `CASTOR_SERVERS__MEDIA__TOKEN`, `CASTOR_SERVERS__SCRAPING__TOKEN`, and `CASTOR_SERVERS__API__TOKEN` in git-ignored `.env`, then run `docker compose up -d`. Remove the `/dev/dri` mapping on hosts without that device. A bridged media container needs `-p 8410:8410` and a device-reachable `servers.media.advertise`.

Image tags: `latest` (stable), `canary` (preview), or a pinned `vX.Y.Z`.

## Contributing and purpose

See [CONTRIBUTING.md](CONTRIBUTING.md) to build or contribute. Castor provides no content, sources, or DRM circumvention. You are responsible for lawful use and compliance with site terms. Provided as-is for lawful, personal and educational use.
