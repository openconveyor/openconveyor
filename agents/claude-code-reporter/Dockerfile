# Reference agent image for the "reporter" archetype.
#
# Contract (see docs/plugin-authoring.md):
#   * ENTRYPOINT reads /task/prompt, reads secrets from /run/secrets/<name>/,
#     exits 0 on success and non-zero on failure.
#   * Binary runs as UID 65532 under a read-only root filesystem. Writable
#     state must land under /tmp (mounted as an emptyDir by conveyor).
#
# The reporter never clones a repo and never pushes code. It reads data from
# the GitHub API, summarizes it with Claude, and posts the result as a GitHub
# Issue comment (or to stdout if no output URL is configured).
#
# Tooling: Claude Code CLI + gh + curl.

FROM node:22-bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        gnupg \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

# conveyor enforces UID 65532 (see internal/controller/build.go:nonRootUID).
# Pre-create the user so HOME resolution inside gh works even on a read-only
# rootfs. All transient state goes to /tmp (the emptyDir mount).
RUN groupadd --system --gid 65532 conveyor \
    && useradd --system --uid 65532 --gid 65532 --home-dir /tmp --shell /usr/sbin/nologin conveyor

COPY --chown=65532:65532 entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER 65532:65532
ENV HOME=/tmp \
    NPM_CONFIG_CACHE=/tmp/.npm

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
