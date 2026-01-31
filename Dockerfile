# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod ./
COPY go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /drl ./main.go

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget

COPY --from=builder /drl /usr/local/bin/drl

ENTRYPOINT ["/usr/local/bin/drl"]
