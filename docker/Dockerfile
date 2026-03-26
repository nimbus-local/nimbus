# ---- Build stage ----
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Download dependencies first (layer cache)
COPY go.mod go.sum* ./
RUN go mod download

# Build both binaries
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o nimbus ./cmd/nimbus
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o nimbuslocal ./cmd/nimbuslocal

# ---- Final image ----
FROM alpine:3.19

# Install aws CLI for use with nimbuslocal, and curl for healthcheck
RUN apk add --no-cache python3 py3-pip curl && \
  pip install awscli --break-system-packages && \
  rm -rf /root/.cache

WORKDIR /app

COPY --from=builder /build/nimbus /usr/local/bin/nimbus
COPY --from=builder /build/nimbuslocal /usr/local/bin/nimbuslocal

# Data directory for S3 objects and metadata
RUN mkdir -p /var/lib/nimbus
VOLUME ["/var/lib/nimbus"]

EXPOSE 4566

HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
  CMD curl -f http://localhost:4566/_nimbus/health || exit 1

ENTRYPOINT ["nimbus"]
