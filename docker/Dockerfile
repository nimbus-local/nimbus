# ---- Build stage ----
FROM golang:1.26-alpine AS builder

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

# aws CLI for nimbuslocal, curl for the healthcheck, and the docker CLI so the
# services that run real containers — ECS tasks and container-image Lambda
# functions — can drive the host daemon through the mounted socket. Without it
# they silently fall back to simulating.
RUN apk add --no-cache python3 py3-pip curl docker-cli && \
  pip install awscli --break-system-packages && \
  rm -rf /root/.cache

WORKDIR /app

COPY --from=builder /build/nimbus /usr/local/bin/nimbus
COPY --from=builder /build/nimbuslocal /usr/local/bin/nimbuslocal

# Data directory for S3 objects and metadata
RUN mkdir -p /var/lib/nimbus
VOLUME ["/var/lib/nimbus"]

# Documents the default port only — NIMBUS_PORT changes the actual listen port.
EXPOSE 4566

# Shell-form CMD so NIMBUS_PORT is expanded at check time.
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
  CMD curl -f "http://localhost:${NIMBUS_PORT:-4566}/_nimbus/health" || exit 1

ENTRYPOINT ["nimbus"]
