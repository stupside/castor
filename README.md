**English** | [简体中文](README.zh-CN.md) | [日本語](README.ja.md)


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

Run Castor as a **native binary** (recommended): it runs on your machine, so it shares your TV's network, which device discovery needs. The binary shells out to three tools that must be on your `PATH`:

| Tool | Version | Used for |
| --- | --- | --- |
| **Chrome / Chromium** | Any recent | Headless stream extraction |
| **ffmpeg** | 7.1+ | Transcoding (and stream copy) |
| **ffprobe** | 7.1+ | Source format detection |

ffmpeg and ffprobe must be **7.1 or newer**: Castor uses flags (`-readrate_initial_burst`, stricter HLS extension handling) that older builds reject. The [Docker image](#docker-optional) bundles a suitable build (Linux only).

### Homebrew (macOS)

```sh
brew install --cask stupside/tap/castor
```

<details>
<summary><b>Build from source</b> (needs Go 1.26+ and cmake)</summary>

The whisper.cpp bindings are cgo and link a locally built `libwhisper.a`, so clone with submodules and build with `make`:

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make          # builds libwhisper.a, then the castor binary
```

`go install` won't work: the vendored whisper.cpp bindings come in through a local `replace` and need that prebuilt static lib.

</details>


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

The commands you'll use most (run `castor --help` for all flags):

| Command | What it does |
| --- | --- |
| `castor scan` | List cast targets on your network |
| `castor cast` | Browse titles and cast, interactively (needs a TMDB key) |
| `castor cast player <url>` | Cast a web page that has an embedded video player |
| `castor cast url <url>` | Cast a direct stream or video URL |
| `castor cast movie <id>` | Resolve a movie id against your sources and cast |
| `castor cast episode <id> --season N --episode N` | Resolve a TV episode and cast |


## Configuration

Castor reads `config.yaml` from the working directory (or `--config <path>`). **The only required key is the `device`** shown in [Quick start](#quick-start); everything else (timeouts, probing, capture, transcoding, network interface, Chrome discovery) has working defaults.

> [!TIP]
> Keep secrets like your TMDB key out of the committed file: put them in a git-ignored `config.local.yaml` that overlays `config.yaml`, or in `CASTOR_SECTION__FIELD` environment variables. See [SECURITY.md](SECURITY.md).

Everything below is optional.

<details>
<summary><b>Sources</b>: resolve movie / episode ids against sites you configure</summary>

`cast movie`, `cast episode`, and the interactive browser resolve a title id against sources you configure. Castor bundles none, so add your own (sites you are authorized to use). There is no catalog and no lookup: Castor substitutes the id into the `templates` you write, prefixes each of your `proxies`, and opens the resulting page, then extracts the stream exactly like `cast player`. For example, `castor cast movie tt12300742` opens `https://your-source.example/embed/movie/tt12300742`.

```yaml
sources:
  - proxies: ["https://your-source.example"]   # base URLs, tried in order
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

</details>

<details>
<summary><b>Resolver</b>: cap resolution with <code>max_height</code></summary>

Source selection and stream probing. All options have sensible defaults; you normally only set `max_height` to your TV's vertical resolution, which caps both the selected stream and the encoder output.

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

</details>

<details>
<summary><b>TMDB</b>: API key for the interactive browser</summary>

The interactive browser (`castor cast`) uses a TMDB API key to search titles; direct commands like `cast movie <id>` do not need it. Get a free key from [themoviedb.org](https://www.themoviedb.org/settings/api) and keep it in `config.local.yaml`:

```yaml
tmdb:
  api_key: "<KEY>"
```

</details>

<details>
<summary><b>Subtitles</b>: auto-transcribe with whisper and burn in (off by default)</summary>

Auto-generated subtitles, transcribed with whisper and burned into the video:

```yaml
whisper:
  enable: true             # off by default
  # language: "fr"         # default: English
  # model_path: ""         # default: ggml-tiny.en (~75 MB, auto-downloaded)
```

</details>


## Supported devices

Run `castor scan` to list what is on your network.

| Protocol | Works with | Status |
| --- | --- | --- |
| **DLNA / UPnP** (`MediaRenderer:1`) | Virtually every smart TV from the last decade (Samsung, LG, Sony Bravia, Panasonic Viera, Philips, Hisense, TCL, VIZIO, Sharp), plus networked players like Kodi, VLC, and Plex | Tested on Samsung |
| **Chromecast** | Google Cast devices | Experimental, untested (contributions welcome) |


## Docker (optional)

The prebuilt `ghcr.io/stupside/castor` image bundles Chrome, ffmpeg, and ffprobe, so you install nothing by hand. Run it on a **Linux host on the same LAN as your TV**.

> [!WARNING]
> `--network host` is required: device discovery (SSDP multicast) and the TV streaming back from Castor both need the container on your real LAN. On Docker Desktop (macOS/Windows) that flag is a no-op, so the container never reaches your TV and `scan` finds nothing. Use the [native binary](#homebrew-macos) there instead.

```sh
# Discover devices (no config needed)
docker run --rm --network host ghcr.io/stupside/castor:latest scan

# Cast, passing the Intel GPU through for hardware transcoding
docker run --rm --network host --device /dev/dri \
  -v "$PWD/config.yaml:/config.yaml" \
  -v castor-cache:/root/.cache \
  ghcr.io/stupside/castor:latest \
  cast player https://example.com/watch/some-video
```

`--device /dev/dri` hands the container your Intel GPU for VA-API hardware H.264 encoding; without it (or on a non-Intel host) Castor falls back to software `libx264`. Either way, when your TV already accepts the source video Castor stream-copies it and skips encoding entirely.

Run commands from the directory holding your [`config.yaml`](config.yaml) (mounted at `/config.yaml`). The `castor-cache` volume persists the auto-downloaded whisper models.

| Tag | Build |
| --- | --- |
| `:latest` | Latest stable release |
| `:canary` | Latest preview build |
| `:v1.7.0` | A specific pinned version |


## Purpose and disclaimer

Castor is a general-purpose caster, not a service tied to any particular site.

- **It hosts nothing.** No bundled video, catalog, or sources: Castor casts only a page, stream URL, or source you supply and are authorized to use, much like a Chromecast.
- **It does not touch DRM.** Castor does not decrypt or circumvent DRM, and cannot cast DRM-protected services.
- **Using it lawfully is your responsibility.** Whether a site's terms of use and your local law allow what you do with Castor is on you. Do not use it to infringe copyright.

Castor is provided as-is for lawful, personal, and educational use.


## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
