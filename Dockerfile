# --- Build Stage ---
FROM golang:1.24 AS builder

# Set the working directory for the build stage
WORKDIR /app

# Copy the Go module files (go.mod and go.sum)
# This helps Docker layers cache dependencies if they don't change.
COPY go.mod ./

# Copy the rest of the application source code
# This includes main.go and the server/ directory.
COPY . .

# Build the Go application
# CGO_ENABLED=0 is crucial for creating a statically linked binary,
# which makes it portable and suitable for a minimal base image like 'alpine' or 'scratch'.
# -o server_app specifies the output executable name.
RUN CGO_ENABLED=0 go build -o epoll ./cmd/epoll/main.go

# --- Runtime Stage ---
FROM alpine:latest

# Set the working directory in the final image
WORKDIR /app

# Copy the built executable from the builder stage
COPY --from=builder /app/epoll .

# Expose the port the server listens on
EXPOSE 8080

# Set the entrypoint for the container
ENTRYPOINT ["/app/epoll"]