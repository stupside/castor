# Debian bookworm (glibc). VA-API H.264 hardware encoding needs the Intel iHD
# user-mode driver (intel-media-va-driver) plus a vaapi-enabled ffmpeg, and only
# a mainstream glibc distro packages both. Chainguard Wolfi, the previous base,
# ships neither a vaapi ffmpeg nor any Intel media driver, so it could only ever
# software-encode with libx264. castor shells out at runtime to ffmpeg/ffprobe
# (transcode + the PCM feed for whisper) and headless chromium (stream
# extractor), and fetches models/APIs over HTTPS. The castor binary is linked
# against glibc 2.17, which bookworm's 2.36 runs; libgomp1/libstdc++6 are the
# whisper cgo runtime and fonts-liberation feeds drawtext + chromium.
#
# Debian (not Ubuntu) because it packages a real chromium .deb; Ubuntu's is a
# snap that will not run headless in a container. non-free carries the Intel
# VA-API driver.
FROM debian:bookworm-slim
ARG TARGETARCH

# Enable the non-free component (the Intel driver lives there), then install the
# runtime. The Intel iHD driver + vainfo are x86-only, so they are added only on
# amd64; on arm64 there is no Intel GPU and DetectH264Encoder falls back to
# libx264. Handles both the deb822 (debian.sources) and classic (sources.list)
# apt layouts so the build survives base-image revisions.
RUN set -eux; \
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
      sed -i 's/^Components: main$/Components: main contrib non-free non-free-firmware/' /etc/apt/sources.list.d/debian.sources; \
    else \
      sed -i 's/ main$/ main contrib non-free non-free-firmware/' /etc/apt/sources.list; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg chromium fonts-liberation libgomp1 libstdc++6 libva2; \
    if [ "$TARGETARCH" = "amd64" ]; then \
      apt-get install -y --no-install-recommends intel-media-va-driver-non-free vainfo; \
    fi; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

# chromium's path in this image; as root in a container it can't use its sandbox.
ENV CASTOR_BROWSER__CHROME_PATH=/usr/bin/chromium \
    CASTOR_BROWSER__HEADLESS=true \
    CASTOR_BROWSER__NO_SANDBOX=true

COPY docker/${TARGETARCH}/castor /usr/local/bin/castor
RUN chmod +x /usr/local/bin/castor
ENTRYPOINT ["castor"]
