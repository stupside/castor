
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

Smart TVs won't cast arbitrary web video, and screen mirroring is laggy and drops resolution. Castor casts the real stream instead, at full quality, from your terminal.

Point Castor at a web page you are watching, or at a direct stream URL, and it finds the video, extracts the stream, transcodes it for your TV, and casts in real time. It can also resolve an IMDB/TMDB id against sources you configure yourself, and burn in auto-generated subtitles.

To extract, it launches headless Chrome and watches network traffic over the Chrome DevTools Protocol, then runs a short action pipeline to start playback: click the page, navigate into the largest iframe, and click again as a fallback. This works on pages that allow automated playback, and won't work everywhere.

*A general-purpose casting tool: it casts only what you point it at. See [Purpose and disclaimer](#purpose-and-disclaimer).*

<p align="center">
  <img src=".github/images/screen-selection.png" alt="Browsing titles in the castor TUI" width="640"/>
  <br/>
  <sub><em>Run <code>castor cast</code> to browse titles and cast, without leaving the terminal.</em></sub>
</p>


## Installation

The recommended way to run Castor is the **native binary**. It runs directly on your machine, so it shares your TV's network (required for device discovery). It requires **Chrome/Chromium** (headless extraction), **ffmpeg** (transcoding), and **ffprobe** (format detection) on your `PATH`. A Linux-only [Docker](#docker-optional) image that bundles all three is also available.

### Homebrew (macOS)

```sh
brew install --cask stupside/tap/castor
```

### From source

Needs Go 1.26+ and cmake (the whisper.cpp bindings are cgo and link a locally built `libwhisper.a`):

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make          # builds libwhisper.a, then the castor binary
```

`go install` won't work: the vendored whisper.cpp bindings come in through a local `replace` and need that prebuilt static lib.


## Quick start

Tell Castor which TV to use: find its name with `castor scan` and put it in `config.yaml`.

```yaml
device:
  name: "Living Room TV"   # exact name from `castor scan`
  type: dlna
```

Now cast a page you are watching, or a stream URL you have:

```sh
castor cast player https://example.com/watch/some-video
```

For the interactive browser, which searches titles and casts them for you, add a TMDB key and a source (see [Configuration](#configuration)), then run `castor cast`:

<p align="center">
  <img src=".github/images/screen-devices.png" alt="Selecting a cast target in the castor TUI" width="640"/>
</p>

Run `castor --help` for every command and its flags.


## Configuration

Castor reads `config.yaml` from the current directory, or the path passed to `--config`. The only required key is the `device` to cast to (shown in [Quick start](#quick-start)); everything mechanical (timeouts, probing, capture, transcoding, network interface, Chrome discovery) has working defaults. Keep secrets out of the committed file: put them in a git-ignored `config.local.yaml` that overlays it, or in `CASTOR_SECTION__FIELD` environment variables. See [SECURITY.md](SECURITY.md).

### Sources

`cast movie`, `cast episode`, and the interactive browser resolve a title id against sources you configure. Castor bundles none, so add your own (sites you are authorized to use). There is no catalog and no lookup: Castor substitutes the id into the `templates` you write, prefixes each of your `proxies`, and opens the resulting page, then extracts the stream exactly like `cast player`. For example, `castor cast movie tt12300742` opens `https://your-source.example/embed/movie/tt12300742`.

```yaml
sources:
  - proxies: ["https://your-source.example"]   # base URLs, tried in order
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

### Resolver

Source selection and stream probing. All options have sensible defaults, you normally only need `max_height` to match your TV's resolution.

```yaml
resolver:
  # The tallest video to cast. Source selection prefers the largest stream no
  # taller than this, and the encoder scales its output down to it. Defaults to
  # 1080; raise it to your TV's native height (e.g. 2160 for a 4K panel) to pass
  # 4K through, or lower it to save bandwidth.
  max_height: 2160
  # hls_timeout: 30s
  # probe_timeout: 30s
  # probe_max_concurrency: 2
  # ffprobe_path: ffprobe
```

Set `max_height` to your TV's vertical resolution. This caps both the selected stream and the encoder output.

### TMDB

The interactive browser (`castor cast`) uses a TMDB API key to search titles; direct commands like `cast movie <id>` do not need it. Get a free key from [themoviedb.org](https://www.themoviedb.org/settings/api) and keep it in `config.local.yaml`:

```yaml
tmdb:
  api_key: "<KEY>"
```

### Subtitles

Auto-generated subtitles, transcribed with whisper and burned into the video. Off by default:

```yaml
whisper:
  enable: true             # off by default
  # language: "fr"         # default: English
  # model_path: ""         # default: ggml-tiny.en (~75 MB, auto-downloaded)
```


## Supported devices

### DLNA / UPnP

Any TV implementing the DLNA/UPnP `MediaRenderer:1` profile works, which covers virtually every smart TV sold in the last decade: **Samsung** (tested), **LG**, **Sony Bravia**, **Panasonic Viera**, **Philips**, **Hisense**, **TCL**, **VIZIO**, **Sharp**. Networked players like Kodi, VLC, and Plex also work. Run `castor scan` to list what is on your network.

### Chromecast

Experimental: implemented but untested. Contributions welcome.


## Docker (optional)

A Linux-only alternative that bundles Chrome, ffmpeg, and ffprobe.

> [!WARNING]
> **Docker can only reach your TV from a Linux host on the same LAN.** Discovery uses SSDP multicast and the TV streams back from Castor's replay server, and neither survives Docker's bridge network, so `--network host` is required. On Docker Desktop (macOS/Windows) that flag is a silent no-op: the container lands on Docker Desktop's internal VM subnet (e.g. `192.168.65.x`) instead of your LAN, so `scan` finds nothing and `cast` fails with `device "…" (type dlna) not found`. Run the [native binary](#homebrew-macos) on macOS/Windows instead, or bridge a Linux VM onto your LAN (e.g. Lima + `socket_vmnet`).

On a Linux box or NAS on the same network as the TV, the prebuilt `ghcr.io/stupside/castor` image saves you installing the dependencies by hand:

```sh
# Discover devices (no config required)
docker run --rm --network host ghcr.io/stupside/castor:latest scan

# Cast, mounting config.yaml and a persistent model cache
docker run --rm --network host \
  -v "$PWD/config.yaml:/config.yaml" \
  -v castor-cache:/root/.cache \
  ghcr.io/stupside/castor:latest \
  cast player https://example.com/watch/some-video
```

The container reads config from [`config.yaml`](config.yaml) at `/config.yaml`, so run every command from the directory holding it. The `castor-cache` volume keeps the auto-downloaded whisper models between runs; swap `:latest` for any release tag to pin a version, or use `:canary` for the latest preview build.

> [!NOTE]
> **DLNA passthrough and hardware encoding.** When the TV can already play the source video (H.264 inside a profile and level it accepts), Castor stream-copies it instead of re-encoding, dropping CPU use to near zero. When a re-encode is unavoidable (a codec the TV rejects, or burned-in subtitles), Castor picks a hardware H.264 encoder if one actually works on the host (VA-API on an Intel Linux box, VideoToolbox on the native macOS binary) and otherwise falls back to software `libx264`. To let the Docker container reach an Intel GPU for VA-API, add `--device /dev/dri` to `docker run` (the `--network host` flag alone does not expose the GPU); without it, or on a host with no working hardware encoder, Castor stays on `libx264`.


## Purpose and disclaimer

Castor is a general-purpose caster, not a service tied to any particular site.

- **It hosts nothing.** No bundled video, catalog, or sources: Castor casts only a page, stream URL, or source you supply and are authorized to use, much like a Chromecast.
- **It does not touch DRM.** Castor does not decrypt or circumvent DRM, and cannot cast DRM-protected services.
- **Using it lawfully is your responsibility.** Whether a site's terms of use and your local law allow what you do with Castor is on you. Do not use it to infringe copyright.

Castor is provided as-is for lawful, personal, and educational use.


## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
