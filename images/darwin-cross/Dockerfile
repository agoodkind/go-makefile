FROM ghcr.io/goreleaser/goreleaser-cross:v1.26.3@sha256:7fa2f6adefc63b9d51daa5678c1f37349583e55bfa0ebead1665443014d346d0

LABEL org.opencontainers.image.source="https://github.com/agoodkind/go-makefile"
RUN apt-get update \
    && apt-get install -y --no-install-recommends ccache \
    && rm -rf /var/lib/apt/lists/*
