# --- Build Stage ---
FROM golang:1.24 AS builder

# Set the working directory for the build stage.
WORKDIR /app

# Copy the Go module files (go.mod and go.sum) to the working directory.
COPY go.mod ./

# Copy the rest of the application source code into the working directory
# This includes the main.go file and any other source files in the project.
COPY . .

# CGO_ENABLED=0 is crucial for creating a statically linked binary,
# which makes it portable and suitable for a minimal base image like 'alpine' or 'scratch'.
RUN CGO_ENABLED=0 go build -o epoll ./cmd/epoll/main.go

# --- Runtime Stage ---
FROM alpine:latest

# Set the working directory in the final image.
WORKDIR /app

# Copy the built executable from the builder stage to the working directory.
COPY --from=builder /app/epoll .

# PORT specifies the port the server will listen on.
ENV PORT=8080

# MAX_LISTENERS sets the maximum number of listeners for the server.
ENV MAX_LISTENERS=10

# Expose the port the server listens on to allow external access.
EXPOSE ${PORT}

RUN echo "Port: ${PORT}, Max Listeners: ${MAX_LISTENERS}"

# # Set the entrypoint for the container
# ENTRYPOINT ["/app/epoll"]
# CMD ["--port", "${PORT}", "--maxListeners", "${MAX_LISTENERS}"]

# Create a startup script.
RUN echo '#!/bin/sh' > /app/start.sh && \
    echo 'exec /app/epoll --port $PORT --maxListeners $MAX_LISTENERS' >> /app/start.sh && \
    chmod +x /app/start.sh

# Use the startup script as the entrypoint.
ENTRYPOINT ["/app/start.sh"]
