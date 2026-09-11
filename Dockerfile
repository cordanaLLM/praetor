FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY praetor /
USER 65532:65532
ENTRYPOINT ["/praetor"]
