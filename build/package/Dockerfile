# syntax=docker/dockerfile:1.4
# Stage 1: Hermetic builder
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=v1.0.0

WORKDIR /workspace

# Install security certificates and git
RUN apk add --no-cache ca-certificates=20260909-r0 git=2.54.0-r0

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
#
# ADR-0005 decision 2 still reads `gcr.io/distroless/static-debian12:nonroot` and its
# decision 1 still reads `golang:1.24-alpine`. Both are superseded by the lines in this
# file. The ADR is Accepted and therefore frozen, so the contradiction is recorded here
# rather than edited there; a reader of ADR-0005 alone would otherwise have nothing
# telling them it is stale. The Debian and Go majors are the only part that moved.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

LABEL org.opencontainers.image.title="praetor" \
      org.opencontainers.image.description="Autonomous Fleet Governance & Workstation Sentinel Engine" \
      org.opencontainers.image.vendor="cordanaLLM" \
      org.opencontainers.image.licenses="EUPL-1.2" \
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
CMD ["serve", "-addr=:8080"]
