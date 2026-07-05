FROM debian:bookworm-slim

# castor shells out at runtime to ffmpeg/ffprobe (transcode + the PCM feed for
# whisper) and headless chromium (stream extractor), and fetches models/APIs
# over HTTPS; libgomp1/libstdc++6 are the whisper cgo runtime.
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg chromium fonts-liberation libgomp1 libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

# chromium's path in this image; as root in a container it can't use its sandbox.
ENV CASTOR_BROWSER__CHROME_PATH=/usr/bin/chromium \
    CASTOR_BROWSER__HEADLESS=true \
    CASTOR_BROWSER__NO_SANDBOX=true

COPY docker/${TARGETARCH}/castor /usr/local/bin/castor
RUN chmod +x /usr/local/bin/castor
ENTRYPOINT ["castor"]
