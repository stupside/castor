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

---

## Contributing

Castor might be unstable but works for simple use cases. If you want to help make it better, feel free to open a PR.

- [ ] Subtitles support
- [ ] Devtool record plugins
- [ ] Play from a specific position
- [ ] Seek actions on the TV (really needed)
- [ ] Chromecast support (couldn't test and implement it properly due to lack of a device)
