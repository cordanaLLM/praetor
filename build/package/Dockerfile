# syntax=docker/dockerfile:1.4
# Stage 1: Hermetic builder
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=v1.0.0

WORKDIR /workspace

# Install security certificates and git
RUN apk add --no-cache ca-certificates git

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source tree
COPY . .

# Build statically linked binary with stripped symbols
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -extldflags '-static'" \
    -o /out/praetorctl ./cmd/standardsctl

# Stage 2: Distroless minimal runtime (< 30MB, zero shell, non-root)
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="praetor" \
      org.opencontainers.image.description="Autonomous Fleet Governance & Workstation Sentinel Engine" \
      org.opencontainers.image.vendor="cordanaLLM" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/cordanaLLM/praetor"

WORKDIR /workspace

# Copy binary from builder
COPY --from=builder /out/praetorctl /usr/local/bin/praetorctl
COPY --from=builder /out/praetorctl /usr/local/bin/standardsctl

# Run as standard distroless nonroot user
USER 65532:65532

# Health check port
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/praetorctl"]
CMD ["audit"]
