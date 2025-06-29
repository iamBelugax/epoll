# Epoll-Based Multithreaded TCP Echo Server in Go

## Overview

This project implements a high-performance, multi-threaded TCP echo server in
Go, specifically designed for Linux environments. It utilizes the `epoll` I/O
event notification facility for efficient handling of numerous concurrent
connections. The server employs a multi-listener architecture with
`SO_REUSEPORT` for kernel-level load balancing across multiple worker
goroutines, providing a simple echo service.

## Architecture Explained

This server leverages Go's concurrency combined with low-level Linux system
calls for optimal network performance. The architecture revolves around a
central `Server` orchestrator managing multiple independent `Worker` goroutines.

### Core Components

1.  **`main` (`main.go`):**

    - **Initialization:** Parses command-line flags (`--port`, `--maxListeners`)
      to configure the server. It sets defaults if flags are not provided (port
      8080, listeners equal to CPU cores).
    - **Server Lifecycle:** Creates an instance of the `epoll.Server`. It starts
      the server using `ListenAndServe()` and ensures graceful shutdown via
      `Stop()` using a `defer` statement.
    - **Signal Handling:** Listens for operating system signals (`SIGINT`,
      `SIGTERM`). Upon receiving a signal, it logs the event, and the deferred
      `Stop()` call initiates the shutdown process.

2.  **`Server` (`internal/epoll/epoll.go`):**

    - **Orchestration:** Acts as the central coordinator. It holds configuration
      details like port and the number of workers (`maxListeners`).
    - **Worker Management:** Creates the specified number of `worker.Worker`
      instances during initialization (`NewServer`). Each worker is configured
      with a unique ID and the shared port.
    - **Concurrency Control:** Uses a `sync.WaitGroup` (`wg`) to manage the
      lifecycle of worker goroutines.
    - **Starting Workers:** The `ListenAndServe` method launches each `Worker`
      in its own goroutine and increments the `WaitGroup` counter. Each worker
      then calls its `Start()` method.
    - **Graceful Shutdown:** The `Stop` method iterates through all workers and
      calls their individual `Stop` methods, which triggers the wake-up
      mechanism. It then waits on the `WaitGroup` (`wg.Wait()`) to ensure all
      worker goroutines have fully completed their shutdown before returning.

3.  **`Worker` (`internal/worker/worker.go`):**
    - **Independent Unit:** Each worker runs as an independent entity within its
      own goroutine.
    - **Socket Setup (`Start` method):**
      - Creates a TCP listening socket (`syscall.Socket`).
      - Sets socket options: `SO_REUSEADDR` (allows reusing local addresses
        quickly) and crucially `SO_REUSEPORT` (allows multiple sockets to bind
        to the _exact same_ address and port) using `syscall.SetsockoptInt`.
        `SO_REUSEPORT` enables the kernel to distribute incoming connections
        across the multiple worker sockets listening on the same port, providing
        efficient load balancing.
      - Binds the socket to the specified port and `0.0.0.0` (`syscall.Bind`).
      - Starts listening for incoming connections (`syscall.Listen`).
    - **Epoll Setup (`Start` method):**
      - Creates a dedicated epoll instance for this worker using
        `syscall.EpollCreate1`.
      - Registers its listening socket (`listenFd`) with the epoll instance
        using `syscall.EpollCtl` (`EPOLL_CTL_ADD`), monitoring for incoming
        connection events (`EPOLLIN`).
    - **Wake-up Mechanism:**
      - Each worker creates a pipe (`syscall.Pipe`) during initialization
        (`New`). The read end of this pipe (`wakeUpRfd`) is also added to the
        worker's epoll instance, monitoring for read events (`EPOLLIN`).
      - The `Worker.Stop` method writes a single byte to the write end of the
        pipe (`wakeUpWfd`). This makes the read end readable, generating an
        epoll event that wakes up the worker from the potentially blocking
        `EpollWait` call. The read end is set to non-blocking
        (`syscall.SetNonblock`) to ensure reading the wake-up byte doesn't
        block.
    - **Resource Management:** Uses `defer` statements and explicit calls to
      `syscall.Close` to ensure listening sockets, client sockets, the epoll
      file descriptor, and wake-up pipe file descriptors are closed during
      shutdown or error handling.

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

## Worker Event Loop Explained (`worker.Start`)

The core logic of each worker resides within its `Start` method, specifically in
the event loop that processes I/O events detected by epoll.

1.  **Wait for Events:** The loop starts by blocking on
    `syscall.EpollWait(epollFd, events, -1)`. It waits indefinitely (`-1`
    timeout) for events on any of the file descriptors registered with this
    worker's `epollFd`. The ready events are returned in the `events` slice.
    `EpollWait` can be interrupted by signals (`EINTR`) or, crucially, by the
    wake-up pipe being written to.

2.  **Check for Shutdown Signal:** After `EpollWait` returns, the code checks if
    any of the returned events correspond to the worker's wake-up pipe's read
    file descriptor (`wakeUpRfd`). If found, it signifies that `Server.Stop()`
    was called.

3.  **Process Events:** The code iterates through the `n` events returned by
    `EpollWait`:

    - **Wake-up Event (`fd == wakeUpRfd`):**
      - If the event is on the wake-up pipe, it reads the byte(s) from the pipe
        to clear the event state.
      - It logs that it was woken up.
      - Crucially, the `isShuttingDown` flag (determined before the loop) will
        be true, causing the loop to break after processing all current events.
        The pipe file descriptors are closed (`closeReadWritePipe`).
    - **New Connection Event (`fd == listenFd`):**
      - If the event is on the listening socket _and_ the worker is _not_
        shutting down:
        - It accepts the new connection using `syscall.Accept`, getting a
          `clientFd`.
        - It sets the new `clientFd` to non-blocking mode
          (`syscall.SetNonblock`).
        - It adds the `clientFd` to the epoll instance using `syscall.EpollCtl`
          (`EPOLL_CTL_ADD`), monitoring for read events (`EPOLLIN`) in
          edge-triggered mode (`EPOLLET`).
      - If the worker _is_ shutting down, it removes the listening socket from
        epoll (`EPOLL_CTL_DEL`) to stop accepting new connections.
    - **Client Socket Event (`else` block):**
      - **Read Ready (`event.Events & syscall.EPOLLIN`):** If the event
        indicates data is available to read on a client socket, it calls
        `handleClient(epollFd, fd)`.
        - `handleClient` reads data in a loop using `syscall.Read` into a
          buffer. Because of `EPOLLET`, it _must_ continue reading until `Read`
          returns 0 (client closed) or an error like `EAGAIN`/`EWOULDBLOCK` (no
          more data currently available).
        - Any data read is echoed back using `syscall.Write`.
        - If `Read` returns 0 or encounters an error (other than
          `EAGAIN`/`EWOULDBLOCK`), the connection is closed via `closeClient`.
        - If the worker is shutting down, the client connection is closed via
          `closeClient` _after_ handling any pending data.
      - **Error/Hangup
        (`event.Events & (syscall.EPOLLHUP | syscall.EPOLLERR)`):** If the event
        indicates an error or hangup on the client socket, the connection is
        closed via `closeClient(epollFd, fd)`. `closeClient` removes the
        `clientFd` from epoll (`EPOLL_CTL_DEL`) and closes the socket
        (`syscall.Close`).

4.  **Loop Termination:** After processing all events in the current batch, if
    the `isShuttingDown` flag was set (due to a wake-up event), the `for` loop
    breaks, and the `worker.Start` method returns, allowing the goroutine to
    exit. The `WaitGroup` counter is decremented via `defer s.wg.Done()` in
    `Server.ListenAndServe`.

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

### Using Docker (if a Dockerfile is available)

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
