# The Go stage runs on the build machine's own platform and cross-compiles, so
# an arm64 Mac builds the amd64 server image without emulating the Go toolchain
# (which crashes under qemu). sqlite needs cgo, hence the C cross-compiler.
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS builder
ARG TARGETARCH
RUN case "$TARGETARCH" in amd64) cc=x86_64-linux-gnu-gcc pkg=gcc-x86-64-linux-gnu ;; arm64) cc=aarch64-linux-gnu-gcc pkg=gcc-aarch64-linux-gnu ;; *) exit 1 ;; esac \
    && if [ "$TARGETARCH" = "$(go env GOARCH)" ]; then cc=gcc; else \
         apt-get update && apt-get install -y --no-install-recommends $pkg libc6-dev-$TARGETARCH-cross && rm -rf /var/lib/apt/lists/*; fi \
    && echo $cc > /cc
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN export CC=$(cat /cc) GOARCH=$TARGETARCH CGO_ENABLED=1 \
    && go build -trimpath -o /arrdio ./cmd/acquisition \
    && go build -trimpath -o /arrdio-migrate ./cmd/migrate

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg libarchive-tools && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home arrdio && mkdir /data && chown arrdio:arrdio /data
COPY --from=builder /arrdio /usr/local/bin/arrdio
COPY --from=builder /arrdio-migrate /usr/local/bin/arrdio-migrate
USER arrdio
ENV ARRDIO_LISTEN=0.0.0.0:2131 ARRDIO_JOBS_DB=/data/jobs.db ARRDIO_STAGING=/data/staging
EXPOSE 2131
CMD ["arrdio"]
