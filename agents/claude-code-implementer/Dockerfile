# Reference agent image for the "implementer" archetype.
#
# Contract (see docs/plugin-authoring.md):
#   * The ENTRYPOINT reads /task/prompt and reads the secrets from
#     /run/secrets/<name>/. It exits 0 on success and non-zero on failure.
#   * The binary runs as UID 65532 under a read-only root filesystem. The
#     entrypoint must write all state under /tmp. conveyor mounts /tmp as an
#     emptyDir.
#
# Tooling: the Claude Code CLI, git and gh. The image supports only GitHub as
# the git host until the project releases the `conveyor-git` helper
# (post-v0.1.0).

# The FROM line pins the base image to a minor tag plus a digest. Dependabot
# (docker ecosystem, agents/*) updates both together.
FROM node:26.12.1-bookworm-slim@sha256:28147155d47dc31abe0a46e0d12b59b2fb3d0a2dcc5dc65fce76a074180b16fe

# The Claude Code CLI version is identical across the three reference agents.
# A maintainer updates it by hand in chore(agents) PRs.
ARG CLAUDE_CODE_VERSION=2.1.311

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

RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}"

# conveyor enforces UID 65532 (see internal/controller/build.go:nonRootUID).
# Create the user in the image so that git and gh can resolve HOME on a
# read-only rootfs. All transient state goes to /tmp (the emptyDir mount).
RUN groupadd --system --gid 65532 conveyor \
    && useradd --system --uid 65532 --gid 65532 --home-dir /tmp --shell /usr/sbin/nologin conveyor

COPY --chown=65532:65532 entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER 65532:65532
ENV HOME=/tmp \
    NPM_CONFIG_CACHE=/tmp/.npm \
    GIT_HOST=github

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
