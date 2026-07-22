# Debian 13 "trixie" (current stable). ffmpeg 7.1 with full VAAPI
# support (h264/hevc/av1/vp9 encoders) and chromium as a proper .deb.
# Requires buildx for multi-arch: TARGETARCH is set by buildx, not by
# plain docker build.  For VAAPI pass --device /dev/dri:/dev/dri.
FROM debian:trixie-slim
ARG TARGETARCH

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates chromium ffmpeg fonts-liberation libgomp1 libstdc++6 libva2; \
    if [ "$TARGETARCH" = "amd64" ]; then \
      apt-get install -y --no-install-recommends intel-media-va-driver vainfo; \
    fi; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

ENV CASTOR_BROWSER__CHROME_PATH=/usr/bin/chromium \
    CASTOR_BROWSER__HEADLESS=true \
    CASTOR_BROWSER__NO_SANDBOX=true

COPY docker/${TARGETARCH}/castor /usr/local/bin/castor
RUN chmod +x /usr/local/bin/castor
ENTRYPOINT ["castor"]
