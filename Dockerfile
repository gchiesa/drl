# ── Stage 1: build the Svelte UI ─────────────────────────────────────────────
FROM node:22-alpine AS ui-builder

WORKDIR /ui

# Install dependencies first (layer-cached as long as package files don't change)
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --prefer-offline

# Copy source and build; vite-plugin-singlefile produces a single index.html
COPY ui/ ./
RUN npm run build

# ── Stage 2: build the Go binary ─────────────────────────────────────────────
FROM golang:1.25-alpine AS go-builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

RUN apk add --no-cache git

# Download Go dependencies first (layer-cached)
COPY go.mod go.sum* ./
RUN go mod download

# Copy Go source
COPY . .

# Overwrite the stub with the real Svelte build artifact
COPY --from=ui-builder /ui/dist/index.html ./internal/api/resources/index.html

# Build the binary (CGO disabled for a fully static binary)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /drl ./main.go

# ── Stage 3: minimal runtime image ───────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget curl

COPY --from=go-builder /drl /usr/local/bin/drl

ENTRYPOINT ["/usr/local/bin/drl"]
