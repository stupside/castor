# Security

Castor collects no telemetry. Logs, traces and metrics leave the machine only when you set `telemetry.endpoint`, and then only to that collector.

## Keeping secrets out of git

Use git-ignored `config.local.yaml` (overlays `config.yaml`):

```yaml
tmdb:
  api_key: "<key>"
```

Environment variables such as `CASTOR_TMDB__API_KEY` override both files. Revoke accidentally committed keys. The TMDB key is used only by the interactive browser and sent only to [TMDB](https://www.themoviedb.org/settings/api).

## API, media, and scraping services

> [!WARNING]
> A standalone server without a token lets anyone who can reach it start casts, or make the scraping server visit any URL, including addresses on its own network. Set a token on every server that listens beyond `localhost`; castor logs a warning when one does not.

- **Authenticate RPCs:** set independent `servers.api.token`, `servers.media.token`, and `servers.scraping.token`; use `Authorization: Bearer <token>`. Health checks are exempt. Default ports `:8411`, `:8410`, and `:8412` bind all interfaces.
- **Restrict network access:** media accepts streams, not webpage inputs, but the API and scraping act on what callers send.
- **Device playback is open:** `/media` cannot require authentication. Anyone who can reach media and knows a cast URL can fetch it; `servers.media.advertise` defines the device-reachable address.
- **Protect credentials in transit:** RPCs use plain HTTP by default and carry tokens, stream URLs, and replay headers. Castor serves plain HTTP only: beyond trusted networks, terminate TLS in a reverse proxy and point clients at its `https://` URL. IP-bound streams may require scraping and media to share an outbound IP.
- **Keep replay headers private:** they reach media but are stripped from public cast listings.

## Headless Chrome

Only scraping runs Chrome and reads `browser`/`capture` settings. It uses an isolated profile and fresh randomized fingerprint, not your browser's cookies or passwords. It visits supplied pages and their loaded resources, following only HTTP(S) frames.

## Image provenance

Release images include an SPDX SBOM and SLSA provenance from the [delivery workflow](.github/workflows/_delivery.yml):

```sh
docker buildx imagetools inspect ghcr.io/stupside/castor:latest --format '{{ json .SBOM }}'
docker buildx imagetools inspect ghcr.io/stupside/castor:latest --format '{{ json .Provenance }}'
```

## Reporting a vulnerability

Report it privately through [GitHub's vulnerability reporting](https://github.com/stupside/castor/security/advisories/new), so it stays out of public view until it is fixed. For anything that is not sensitive, a regular [issue](https://github.com/stupside/castor/issues) is fine.
