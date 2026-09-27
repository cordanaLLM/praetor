# Release image. GoReleaser's dockers_v2 block (.goreleaser.yaml) builds it in the release
# job from the praetorctl binaries that job already compiled, so the binary in the image is
# the one in the release archive and is compiled exactly once (HISS-19). The build context
# holds one <os>/<arch>/praetorctl per platform and no source tree, which is why a plain
# `docker build .` in a checkout has nothing to copy; `goreleaser release --snapshot --clean`
# builds the image locally (docs/guides/releasing.md). ADR-0013 records this as the one
# image path.
#
# The runtime digest is repeated in templates/go/Dockerfile.distroless.tmpl, the body the
# go-service flavor scaffolds; internal/supplychain/image_pins_test.go fails when the copies
# disagree.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

ARG TARGETPLATFORM

# The OCI labels (title, source, licenses, version, revision) come from the dockers_v2
# block, which writes them once for both the image labels and the manifest-list
# annotations.

WORKDIR /workspace

# standardsctl is the pre-rename name of the same program; .goreleaser.yaml keeps both.
COPY $TARGETPLATFORM/praetorctl /usr/local/bin/praetorctl
COPY $TARGETPLATFORM/praetorctl /usr/local/bin/standardsctl

# The distroless nonroot user; the Helm chart's podSecurityContext runs as the same UID.
USER 65532:65532

# praetorctl serve answers /healthz, /livez and /readyz on this port.
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/praetorctl"]
CMD ["serve", "-addr=:8080"]
