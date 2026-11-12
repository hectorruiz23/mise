FROM --platform=$BUILDPLATFORM node:26-bookworm-slim AS panel
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
COPY --from=panel /src/web/dist ./internal/panel/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/mise ./cmd/mise

FROM alpine:3.21
WORKDIR /app
COPY --from=build /out/mise /app/mise
USER 1000:1000
ENV MISE_ADDR=0.0.0.0:8787 \
    MISE_DB=/data/mise.db \
    MISE_LOG_LEVEL=info
VOLUME ["/data"]
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8787/api/health || exit 1
ENTRYPOINT ["/app/mise"]
