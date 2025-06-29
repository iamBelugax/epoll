# Epoll-Based Multithreaded TCP Echo Server in Go

## Overview

This project implements a high-performance, multi-threaded TCP echo server in
Go, specifically designed for Linux environments. It utilizes the `epoll` I/O
event notification facility for efficient handling of numerous concurrent
connections. The server employs a multi-listener architecture with
`SO_REUSEPORT` for kernel-level load balancing across multiple worker
goroutines, providing a simple echo service.

### Key Architectural Concepts

- **`SO_REUSEPORT` Load Balancing:** This is central to the multi-worker design.
  Instead of a single listening socket distributing connections, multiple
  workers _each_ have their own listening socket on the same port. The Linux
  kernel distributes incoming connections across these sockets, typically using
  a hash of the connection's IP addresses and ports, achieving efficient,
  contention-free load balancing at the kernel level.
- **Dedicated Epoll per Worker:** Each worker manages its own set of file
  descriptors (its listener, its clients, its wake-up pipe) using its own epoll
  instance. This avoids contention on a single epoll instance and scales well
  with the number of cores/workers.
- **Non-Blocking I/O:** All sockets (listening and client) are set to
  non-blocking mode (`syscall.SetNonblock`). This prevents I/O operations like
  `accept`, `read`, and `write` from blocking the worker goroutine, allowing it
  to remain responsive to other events managed by epoll.
- **Edge-Triggered Epoll (`EPOLLET`):** Client sockets are monitored using
  edge-triggered mode (`EPOLLIN | EPOLLET`). This means epoll only notifies the
  worker when a _new_ event occurs (e.g., data arrives on a previously empty
  buffer). It requires the application to process the event source completely
  (e.g., read _all_ available data) until it would block (returns
  `EAGAIN`/`EWOULDBLOCK`). This is generally more performant than
  level-triggered mode under high load as it reduces the number of `epoll_wait`
  wake ups.

## Prerequisites

- Go (version 1.18 or later recommended)
- Linux Operating System (due to the use of the Linux-specific `epoll` API)
- Docker (optional, for containerized build and run)

## Build and Run

### Directly with Go

1.  **Clone:**

    ```bash
    git clone https://github.com/iamNilotpal/epoll.git
    cd epoll
    ```

2.  **Build:**

    ```bash
    go build -o epoll-server ./cmd/epoll/main.go
    ```

3.  **Run:**
    ```bash
    ./epoll-server [flags]
    ```

### Using Docker (Preferred)

1.  **Build Image:**

    ```bash
    docker build -t epoll-server .
    ```

2.  **Run Container:**

    ```bash
    # Run with default settings (port 8080)
    docker run -p 8080:8080 --rm --name my-epoll-server epoll-server

    # Run with custom port and listener count
    docker run -p 9000:9000 --rm --name my-epoll-server epoll-server --port 9000 --maxListeners 16
    ```

## Configuration

The server can be configured using command-line flags:

- `--port <number>`: Specifies the TCP port number the server should listen on.
  - Default: `8080`
- `--maxListeners <number>`: Sets the number of concurrent listener/worker
  goroutines to start.
  - Default: Number of logical CPUs available (`runtime.NumCPU()`)

Example:

```bash
./epoll-server --port 9999 --maxListeners 4
```

## Testing the Server

You can test the server using tools like `netcat` or `telnet`:

```bash
# Connect to the server
nc localhost 8080

# Type a message and press Enter
# The server will echo back the message
```
