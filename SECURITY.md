# Security

How Castor handles secrets, your local network, and the headless browser it launches. Castor collects no telemetry and sends nothing to its author.

## Keeping secrets out of git

Castor uses two config files: `config.yaml` (committed) and `config.local.yaml` (git-ignored). **Put all secrets in `config.local.yaml`**: it overlays the main config and is never tracked.

```yaml
# config.local.yaml  ← git-ignored, safe for secrets
tmdb:
  api_key: "your-real-key-here"
```

Alternatively, export secrets as environment variables (they take precedence over both files):

```sh
export CASTOR_TMDB__API_KEY="your-real-key-here"
```

Never put real keys in `config.yaml`. If you accidentally commit one, revoke it immediately at the issuing service and rotate.

## TMDB API key

The TMDB key is only used by the interactive `castor cast` browser to query the TMDB API. It is never sent anywhere else. A free key has a generous rate limit and no billing exposure, but it is still a credential tied to your account.

Get or revoke keys at [themoviedb.org/settings/api](https://www.themoviedb.org/settings/api).

## Local network stream server

When casting, Castor starts a temporary HTTP server on your machine to serve the transcoded stream to the TV. This server:

- Binds to the local network interface only (auto-detected from the default route, or pinned via `network.interface` in config)
- Serves a single stream for the duration of playback
- Has no authentication; anyone on the same network can fetch the stream URL if they know it

This is intentional: DLNA renderers cannot authenticate. Keep Castor on a trusted home network.

## Headless Chrome

Castor launches Chrome to extract streams. The browser process:

- Runs fully headless and isolated from your real browser: no access to your default Chrome profile, cookies, or saved passwords
- Uses a fresh, randomized browser fingerprint per extraction run, not derived from or linked to your real browser or identity
- Only visits the page you point it at (plus whatever that page itself loads)

## Supply chain and image provenance

The `ghcr.io/stupside/castor` image is built and published only by the [release workflow](.github/workflows/_delivery.yml), never by hand. Every published image ships with an **SPDX SBOM** and full **SLSA build provenance**, attached to the manifest at build time.

Read them with `docker buildx imagetools inspect` (any tag or `@sha256:...` digest works):

```sh
docker buildx imagetools inspect ghcr.io/stupside/castor:latest --format '{{ json .SBOM }}'
docker buildx imagetools inspect ghcr.io/stupside/castor:latest --format '{{ json .Provenance }}'
```

## Reporting a vulnerability

Open a [GitHub issue](https://github.com/stupside/castor/issues) marked **[security]**. For sensitive reports, contact the maintainer directly via the email on their GitHub profile.
