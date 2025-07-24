###############  base image shared by both stages  ###############
FROM mcr.microsoft.com/playwright:v1.51.1-jammy AS pw-base
WORKDIR /src

##################################################################
#                 ----------  build stage ----------             #
##################################################################
FROM pw-base AS builder

# 1️⃣  install Go 1.24.1
RUN curl -fsSL https://go.dev/dl/go1.24.1.linux-amd64.tar.gz \
      | tar -C /usr/local -xz

ENV PATH="/usr/local/go/bin:${PATH}" \
    CGO_ENABLED=1

# 2️⃣  minimal C tool‑chain (no LLVM monster)
RUN apt-get update && \
    apt-get install -y --no-install-recommends gcc g++ libc6-dev make && \
    rm -rf /var/lib/apt/lists/*

# 3️⃣  build the binary
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /app/server .

##################################################################
#                ----------  runtime stage ----------            #
##################################################################
FROM pw-base

ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright

WORKDIR /app
COPY --from=builder /app/server .

# Writable dir for SQLite
RUN mkdir -p /app/data && chown -R pwuser:pwuser /app /ms-playwright
USER root
WORKDIR /app/data

EXPOSE 8080
CMD ["/app/server"]
