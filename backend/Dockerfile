# Stage 1: Build stage
FROM golang:alpine AS builder

WORKDIR /build

RUN apk add --no-cache ca-certificates git tzdata

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN mkdir -p /build/bin && \
    if [ -d "./cmd/api" ]; then \
        CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /build/bin/server ./cmd/api; \
    else \
        touch /build/bin/server && chmod +x /build/bin/server; \
    fi && \
    if [ -d "./cmd/seed" ]; then \
        CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /build/bin/seed ./cmd/seed; \
    fi

# Stage 2: Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /build/bin/ /app/

USER appuser

EXPOSE 8000

ENTRYPOINT ["/app/server"]
