# syntax=docker/dockerfile:1
# Multi-arch build (linux/amd64, linux/arm64). Build context is the repo root.
# Targets: runtime (default, distroless, published as :<version>) and media (adds ffmpeg for video
# transcoding, published as :<version>-media). Both run as uid 65532 so a /data volume works with
# either.

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
RUN corepack enable
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/.pnpmfile.cjs ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN mkdir -p ../server/internal/webui/dist && pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS server
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=none
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X github.com/voidrot/chorecast/server/internal/version.Version=${VERSION} -X github.com/voidrot/chorecast/server/internal/version.Commit=${COMMIT}" \
      -o /out/chorecast ./cmd/chorecast

FROM alpine:3.24 AS media
RUN apk add --no-cache ffmpeg ca-certificates \
 && adduser -D -H -u 65532 nonroot \
 && mkdir /data && chown 65532:65532 /data
COPY --from=server /out/chorecast /chorecast
USER 65532:65532
ENV CHORECAST_DATA_DIR=/data CHORECAST_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/chorecast"]
CMD ["serve"]

# Last stage = default target.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=server /out/chorecast /chorecast
ENV CHORECAST_DATA_DIR=/data CHORECAST_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/chorecast"]
CMD ["serve"]
