package p

// HISS-11: a floating container tag. The same string resolves to different bytes on
// every pull, so the deployment is not reproducible and no attestation can bind it.
const SidecarImage = "docker.io/library/redis:latest"
