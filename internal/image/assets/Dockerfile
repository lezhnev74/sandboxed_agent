# The sandboxed_agent image: one box per repo runs this. Embedded in the agent
# binary and built with a tar context on stdin (see internal/image).
FROM debian:trixie-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg git openssh-client less procps sudo tini \
      ripgrep jq unzip xz-utils build-essential python3 nodejs npm postgresql-client \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
 && echo "deb [signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" \
      > /etc/apt/sources.list.d/docker.list \
 && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin \
 && rm -rf /var/lib/apt/lists/*

# The box runs as the host user, so files written in the workspace stay the
# host user's. The gid may already exist in the image (e.g. 100 users).
ARG UID=1000
ARG GID=1000
RUN (getent group "$GID" || groupadd -g "$GID" agent) \
 && useradd -o -u "$UID" -g "$GID" -m -d /home/agent -s /bin/bash agent \
 && echo 'agent ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/agent \
 && chmod 0440 /etc/sudoers.d/agent

COPY sandboxed_agent-exec sandboxed_agent-idle /usr/local/bin/

# mise installs the repo's toolchains (mise.toml, .tool-versions, go.mod)
# into the sandboxed_agent-mise volume at ~/.local/share/mise. Its shims come first
# on PATH, for shells and exec'd harnesses alike. Every config is trusted:
# the box is the boundary.
RUN curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && mise --version
ENV PATH=/home/agent/.local/share/mise/shims:$PATH \
    MISE_DATA_DIR=/home/agent/.local/share/mise \
    MISE_TRUSTED_CONFIG_PATHS=/ \
    MISE_YES=1 \
    MISE_IDIOMATIC_VERSION_FILE_ENABLE_TOOLS=go

# The harnesses, latest releases (Claude Code `latest` channel, OpenCode
# latest), installed under /opt so any uid can read them. HARNESS_REV
# busts this layer's cache: `sandboxed_agent --rebuild` passes a fresh value.
ARG HARNESS_REV=0
RUN echo "harness rev $HARNESS_REV" \
 && mkdir -p /opt/claude /opt/opencode \
 && HOME=/opt/claude bash -c 'curl -fsSL https://claude.ai/install.sh | bash -s latest' \
 && ln -s "$(readlink -f /opt/claude/.local/bin/claude)" /usr/local/bin/claude \
 && HOME=/opt/opencode bash -c 'curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path' \
 && ln -s /opt/opencode/.opencode/bin/opencode /usr/local/bin/opencode \
 && chmod -R a+rX /opt/claude /opt/opencode

USER agent
WORKDIR /home/agent
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["sleep", "infinity"]
