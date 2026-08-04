# syntax=docker/dockerfile:1

# --- build ---------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first: they change far less often than the code, so this layer
# survives most rebuilds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# CGO off, so the binary carries no libc dependency and can run on a base
# image that has no shell and no libraries.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tedmcp .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go test ./...

# The cache directory is created here, with its ownership already right: the
# runtime image has no shell to mkdir with and no entrypoint script to chown.
RUN install -d -m 0755 -o 65532 -g 65532 /out/cache

# --- run -----------------------------------------------------------------
# distroless/static carries CA certificates — needed to reach TED over HTTPS —
# a nonroot user, and nothing else: no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/tedmcp /usr/local/bin/tedmcp
COPY --from=build --chown=65532:65532 /out/cache /var/cache/tedmcp

# The AGPL is conveyed with the program, so the licence travels inside the
# image rather than being left behind in the repository.
COPY --from=build /src/LICENSE /usr/local/share/tedmcp/license.md

# TED notices never change once published, so this cache is worth keeping
# across restarts: a cold scan of a few hundred notices takes minutes, a warm
# one takes seconds.
VOLUME ["/var/cache/tedmcp"]

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/tedmcp"]

# HTTP by default, because stdio only makes sense when a client owns the
# process (docker run -i, with the arguments overridden).
#
# Binding 0.0.0.0 is what lets a published port reach the process; keep the
# exposure on the host side instead — `-p 127.0.0.1:8080:8080`. The server
# has no authentication of its own.
CMD ["-http", "0.0.0.0:8080", "-cache-dir", "/var/cache/tedmcp"]
