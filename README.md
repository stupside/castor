
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
    <img src="https://img.shields.io/github/actions/workflow/status/stupside/castor/ci.yml?style=flat-square" alt="Build Status">
  </a>
</p>

# Castor

Imagine buying a very nice TV and figuring out it doesn't allow casting from random websites...

- Then you switch to the longest **HDMI cable** you can find.
- Then you start doing **screen mirroring**: your computer lags, resolution tanks, nothing feels right.

Castor is a CLI that extracts video streams from websites, handles format compatibility, and casts to your TV in real time, with optional auto-generated subtitles burned directly into the video.

> [!NOTE]
> **How does extraction work?**
>
> Castor launches a headless Chrome with a randomized browser fingerprint and stealth scripts to hide automation. It listens to all network activity via Chrome DevTools Protocol, captures video streams, then runs an action pipeline: click the page, navigate into the largest iframe, solve Cloudflare Turnstile if detected, and click again as fallback.
>
> This works on most streaming websites but won't beat sophisticated bot protection.


## Supported devices

### DLNA / UPnP

Any TV implementing the DLNA/UPnP `MediaRenderer:1` profile works, which covers virtually every smart TV sold in the last decade: **Samsung** (tested), **LG**, **Sony Bravia**, **Panasonic Viera**, **Philips**, **Hisense**, **TCL**, **VIZIO**, **Sharp**. Networked players like Kodi, VLC, and Plex also work.

Run `castor scan` to discover devices on your network.

### Chromecast

> [!WARNING]
> Experimental: implemented but untested. Contributions welcome.


## Requirements

| Dependency | Purpose |
|---|---|
| **Chrome / Chromium** | Headless stream extraction |
| **ffmpeg** | Transcoding |
| **ffprobe** | Format detection |


## Quick start

```sh
# 1. Find your TV's name
castor scan

# 2. Set it in config.yaml, then browse and cast
castor cast browse --source vidsrc
```

`cast browse` opens a TUI backed by TMDB. Browse trending titles, search, pick a movie or episode, and cast. Requires a free [TMDB API key](https://www.themoviedb.org/settings/api).


## Usage

```sh
# Browse TMDB and cast from a TUI
castor cast browse --source vidsrc

# Cast from a streaming site
castor cast player https://www.fmovies.gd/watch/movie/1315303

# Cast by IMDB ID
castor cast movie   --source vidsrc tt33028778
castor cast episode --source vidsrc tt2699128 --season 1 --episode 3

# Cast a raw stream URL directly
castor cast url https://example.com/stream.m3u8

# Useful flags
castor cast movie --dry-run --source vidsrc tt33028778  # print URLs without casting
castor --debug cast player https://...                  # verbose logging
castor info                                             # version / build info
```


## Configuration

`config.yaml` (override with `--config`). A sibling `config.local.yaml` overlays it and is git-ignored; put API keys there.

```yaml
device:
  name: "Living Room TV"   # exact name from `castor scan`
  type: dlna

sources:
  - name: vidsrc
    proxies: ["https://vidsrc-embed.ru"]
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"

whisper:
  enable: true             # auto-generated subtitles, burned into the video
  # language: "fr"         # default: English
  # model_path: ""         # default: ggml-tiny.en (~75 MB, auto-downloaded)

tmdb:
  api_key: "<YOUR_TMDB_API_KEY>"   # required for `cast browse`
```


## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
