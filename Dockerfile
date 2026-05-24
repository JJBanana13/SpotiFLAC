# --- Stage 1: build the React/Vite frontend ---
FROM node:20-alpine AS frontend
WORKDIR /app
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm run build

# --- Stage 2: build the Go server (with the dist bundle embedded) ---
FROM golang:1.26-alpine AS backend
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Drop the placeholder embed and replace with the real bundle.
RUN rm -rf cmd/server/frontend_dist/* && mkdir -p cmd/server/frontend_dist
COPY --from=frontend /app/dist/. cmd/server/frontend_dist/
ARG VERSION=docker
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /out/spotiflac-server ./cmd/server

# --- Stage 3: minimal runtime ---
FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates tzdata && adduser -D -u 1000 spotiflac
WORKDIR /app
COPY --from=backend /out/spotiflac-server /usr/local/bin/spotiflac-server
RUN mkdir -p /data /tmp/spotiflac && chown -R spotiflac:spotiflac /data /tmp/spotiflac
USER spotiflac
ENV SPOTIFLAC_DATA_DIR=/data \
    SPOTIFLAC_TMP_DIR=/tmp/spotiflac \
    SPOTIFLAC_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/spotiflac-server"]
