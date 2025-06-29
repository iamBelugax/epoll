# Epoll-Based Multithreaded TCP Server

A high-performance, multi-threaded TCP server built in Go using Linux's epoll
event notification system. This server leverages Go's concurrency features along
with system-level optimizations for handling many simultaneous connections
efficiently.

## Architecture

This server implements a specialized architecture that combines Go's concurrency
model with the Linux epoll interface for optimal performance:

### Key Components

- **Multiple Listener Design**: The server creates multiple listener goroutines,
  each with its own socket bound to the same port using `SO_REUSEPORT`. This
  allows the kernel to distribute incoming connections across multiple listener
  sockets, improving concurrency.

- **Epoll-Based Event Loop**: Each listener goroutine creates and manages its
  own epoll instance, monitoring its listener socket and all client connections
  it accepts. This approach uses edge-triggered (`EPOLLET`) notifications for
  maximum efficiency.

- **Non-Blocking I/O**: All sockets are set to non-blocking mode, allowing the
  server to handle I/O operations without blocking goroutines.

- **Graceful Shutdown**: The server implements a clean shutdown mechanism using
  context cancellation and a wake-up pipe that signals to all listener
  goroutines when it's time to stop.

### Performance Benefits

- **Kernel-Level Load Balancing**: By using `SO_REUSEPORT`, the kernel handles
  load balancing of new connections across listener goroutines.

- **Reduced Contention**: Each goroutine manages its own epoll instance and
  client set, minimizing lock contention.

## Prerequisites

- Go 1.18 or later
- Linux-based OS (epoll is a Linux-specific API)
- Docker (optional, for containerized deployment)

## Build and Run

### Directly with Go

1. Clone the repository:

   ```bash
   git clone https://github.com/iamNilotpal/epoll.git
   cd epoll
   ```

2. Build the server:

   ```bash
   go build -o epoll ./cmd/epoll/main.go
   ```

3. Run the server:

   ```bash
   ./epoll --port 8080 --maxListeners 8
   ```

   The flags are optional:

   - `--port`: Port number to listen on (default: 8080)
   - `--maxListeners`: Number of listener goroutines to start (default: number
     of CPU cores)

### Using Docker

1. Build the Docker image:

   ```bash
   docker build -t epoll-server .
   ```

2. Run the container:

   ```bash
   docker run -p 8080:8080 epoll-server
   ```

3. Run with custom configuration:
   ```bash
   docker run -p 9000:9000 -e PORT=9000 -e MAX_LISTENERS=12 epoll-server
   ```

## Testing the Server

You can test the server using tools like `netcat` or `telnet`:

```bash
# Connect to the server
nc localhost 8080

# Type a message and press Enter
# The server will echo back the message
```
