FROM golang:1.27.3-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -o /arrdio ./cmd/acquisition \
    && CGO_ENABLED=1 go build -trimpath -o /arrdio-migrate ./cmd/migrate

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg libarchive-tools && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home arrdio && mkdir /data && chown arrdio:arrdio /data
COPY --from=builder /arrdio /usr/local/bin/arrdio
COPY --from=builder /arrdio-migrate /usr/local/bin/arrdio-migrate
USER arrdio
ENV ARRDIO_LISTEN=0.0.0.0:2131 ARRDIO_JOBS_DB=/data/jobs.db ARRDIO_STAGING=/data/staging
EXPOSE 2131
CMD ["arrdio"]
