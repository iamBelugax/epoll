# Epoll-Based Multithreaded TCP Echo Server in Go

## Build and Run

### Directly with Go

1.  **Clone:**

    ```bash
    git clone https://github.com/iamBelugaa/epoll-test.git
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
