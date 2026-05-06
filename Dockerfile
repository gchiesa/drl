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

# Install swag CLI for OpenAPI doc generation
RUN go install github.com/swaggo/swag/cmd/swag@v1.16.4

# Copy Go source
COPY . .

# Overwrite the stub with the real Svelte build artifact
COPY --from=ui-builder /ui/dist/index.html ./internal/api/resources/index.html

# Generate OpenAPI docs
RUN swag init --parseDependency --parseInternal --md internal/api -g internal/api/api.go -o internal/api/docs

# Build the binary (CGO disabled for a fully static binary)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /drl ./main.go

# ── Stage 3: minimal runtime image ───────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget curl

COPY --from=go-builder /drl /usr/local/bin/drl

ENTRYPOINT ["/usr/local/bin/drl"]
