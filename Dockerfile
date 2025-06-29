# --- Build Stage ---
FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod ./

COPY . .

RUN CGO_ENABLED=0 go build -o epoll ./cmd/epoll/main.go

# --- Runtime Stage ---
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/epoll .

ENV PORT=8080

ENV MAX_LISTENERS=10

EXPOSE ${PORT}

RUN echo "Port: ${PORT}, Max Listeners: ${MAX_LISTENERS}"

RUN echo '#!/bin/sh' > /app/start.sh && \
    echo 'exec /app/epoll --port $PORT --maxListeners $MAX_LISTENERS' >> /app/start.sh && \
    chmod +x /app/start.sh

ENTRYPOINT ["/app/start.sh"]
