# syntax=docker/dockerfile:1
# Multi-arch build (linux/amd64, linux/arm64). Build context is the repo root.

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
RUN corepack enable
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/chorecast /chorecast
ENV CHORECAST_DATA_DIR=/data CHORECAST_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/chorecast"]
CMD ["serve"]
