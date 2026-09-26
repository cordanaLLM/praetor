# syntax=docker/dockerfile:1
# cordanaLLM/praetor Go development container base
FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414

LABEL org.opencontainers.image.title="cordanaLLM/praetor dev container" \
      org.opencontainers.image.description="Go development environment for cordanaLLM/praetor" \
      org.opencontainers.image.licenses="EUPL-1.2" \
      org.opencontainers.image.vendor="cordanaLLM"

# Install git, make, bash, curl, sudo, shadow, base dev tools and certificates
RUN apk update && \
    apk add --no-cache \
      git=2.54.0-r0 \
      make=4.4.1-r4 \
      bash=5.3.9-r1 \
      curl=8.22.0-r0 \
      sudo=1.9.17_p2-r1 \
      shadow=4.18.0-r1 \
      ca-certificates=20260909-r0 \
      tzdata=2026d-r0 \
      openssh-client-default=10.3_p1-r1 \
      tar=1.35-r5 \
      gcc=15.2.0-r5 \
      musl-dev=1.2.6-r2 \
    && rm -rf /var/cache/apk/*

# Configure non-root user vscode (1000:1000) with passwordless sudo
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000

RUN groupadd --gid ${USER_GID} ${USERNAME} \
    && useradd --uid ${USER_UID} --gid ${USER_GID} -m -s /bin/bash ${USERNAME} \
    && echo "${USERNAME} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/${USERNAME} \
    && chmod 0440 /etc/sudoers.d/${USERNAME}

# Set standard environment paths
ENV USER=${USERNAME} \
    HOME=/home/${USERNAME} \
    GOPATH=/home/${USERNAME}/go \
    PATH=/home/${USERNAME}/go/bin:/usr/local/go/bin:${PATH}

# Create GOPATH directory hierarchy with proper permissions
RUN mkdir -p /home/${USERNAME}/go/bin /home/${USERNAME}/go/pkg /home/${USERNAME}/go/src /workspace \
    && chown -R ${USERNAME}:${USERNAME} /home/${USERNAME} /workspace

USER ${USERNAME}
WORKDIR /workspace

CMD ["/bin/bash"]
