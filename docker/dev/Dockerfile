# syntax=docker/dockerfile:1
# cordanaLLM/standards minimal multi-arch devcontainer base (< 250MB)
FROM golang:1.24-alpine3.21

LABEL org.opencontainers.image.title="cordanaLLM/standards dev container" \
      org.opencontainers.image.description="Minimal multi-arch zero-CVE development container for cordanaLLM/standards" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="cordanaLLM"

# Install git, make, bash, curl, sudo, shadow, base dev tools and certificates
RUN apk update && \
    apk add --no-cache \
      git \
      make \
      bash \
      curl \
      sudo \
      shadow \
      ca-certificates \
      tzdata \
      openssh-client \
      tar \
      gcc \
      musl-dev \
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
