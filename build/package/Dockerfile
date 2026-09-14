# syntax=docker/dockerfile:1.4
# Stage 1: Hermetic builder
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=v1.0.0

WORKDIR /workspace

# Install security certificates and git
RUN apk add --no-cache ca-certificates=20260611-r0 git=2.54.0-r0

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source tree
COPY . .

# Build statically linked binary with stripped symbols
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -extldflags '-static'" \
    -o /out/praetorctl ./cmd/praetorctl

# Stage 2: Distroless minimal runtime (< 30MB, zero shell, non-root)
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="praetor" \
      org.opencontainers.image.description="Autonomous Fleet Governance & Workstation Sentinel Engine" \
      org.opencontainers.image.vendor="cordanaLLM" \
      org.opencontainers.image.licenses="EUPL-1.2" \
      org.opencontainers.image.source="https://github.com/cordanaLLM/praetor"

WORKDIR /workspace

# Copy binary from builder
COPY --from=builder /out/praetorctl /usr/local/bin/praetorctl

# Run as standard distroless nonroot user
USER 65532:65532

# Health check port
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/praetorctl"]
CMD ["serve", "-addr=:8080"]
