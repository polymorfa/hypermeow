# syntax=docker/dockerfile:1.7

FROM golang:1.26-bookworm AS build
ARG BUILD_REV=working-tree
WORKDIR /src
COPY . .
WORKDIR /src/benchmark/barback
RUN --mount=type=cache,target=/go/pkg/mod go mod download
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.revision=${BUILD_REV}" -o /out/hypermeow-bench ./cmd/bench

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hypermeow-bench /usr/local/bin/hypermeow-bench
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/hypermeow-bench"]
