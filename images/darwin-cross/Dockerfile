FROM scratch AS archives
ADD --checksum=sha256:d553fac006ed93a37342cee9228cf4e13dd66690fa1a8075c54c7bace9b72ae6 https://github.com/ccache/ccache/releases/download/v4.14.3/ccache-4.14.3-linux-x86_64-musl-static.tar.xz /ccache.tar.xz
ADD --checksum=sha256:83b71181fb62b4fe8f26067a9321448572e7a43bd04ff6c7f54a24c33698f16d https://github.com/Kitware/CMake/releases/download/v4.5.1/cmake-4.5.1-linux-x86_64.tar.gz /cmake.tar.gz

FROM ghcr.io/goreleaser/goreleaser-cross:v1.27.3-4@sha256:0acd8827de9901bd99082eaa846304e74e1d3c60f05c09739feabcc8aa8fdbd8

LABEL org.opencontainers.image.source="https://github.com/agoodkind/go-makefile"

RUN --mount=from=archives,target=/archives tar -xJf /archives/ccache.tar.xz --directory=/usr/local/bin --strip-components=1 --wildcards "*/ccache" \
    && tar -xzf /archives/cmake.tar.gz --directory=/usr/local --strip-components=1
