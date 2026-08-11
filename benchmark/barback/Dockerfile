# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
# Copyright (c) 2026 Rajeh Taher
#
# Licensed under the MIT License. See LICENSE-MIT for details.

FROM golang:1.26-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS build
ARG BUILD_REV=working-tree
ARG BENCH_BUILD_TAGS
WORKDIR /src
COPY . .
COPY --from=library . /library
WORKDIR /src/benchmark/barback
RUN sh ./prepare-library-module.sh /library
RUN --mount=type=cache,target=/go/pkg/mod go mod tidy
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -tags="${BENCH_BUILD_TAGS}" -trimpath -ldflags="-s -w -X main.revision=${BUILD_REV}" -o /out/hypermeow-bench ./cmd/bench

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
COPY --from=build /out/hypermeow-bench /usr/local/bin/hypermeow-bench
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/hypermeow-bench"]
