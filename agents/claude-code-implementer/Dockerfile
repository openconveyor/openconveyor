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
FROM node:26.13.0-bookworm-slim@sha256:add5c17c14752c54d2f3d519ec46a3815684d41c33f198bcb09fb998f7dcbf68

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

# The build context is agents/, so that the image can copy the shared library
# (no shared base image):
#   docker build -f agents/claude-code-implementer/Dockerfile agents/
COPY common/lib.sh /usr/local/lib/conveyor/lib.sh
COPY --chown=65532:65532 claude-code-implementer/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0644 /usr/local/lib/conveyor/lib.sh \
    && chmod +x /usr/local/bin/entrypoint.sh

USER 65532:65532
# gh never prompts and prints no color codes into the logs.
ENV HOME=/tmp \
    GH_PROMPT_DISABLED=1 \
    NO_COLOR=1 \
    NPM_CONFIG_CACHE=/tmp/.npm \
    GIT_HOST=github

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
