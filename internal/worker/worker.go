package worker

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"slices"
	"syscall"
)

var (
	// MAX_EVENTS is the maximum number of events to retrieve in a single epoll_wait call.
	// This limits the batch size of events processed in each iteration of the event loop.
	MAX_EVENTS = 1024

	// MAX_BUFFER_SIZE is the buffer size for reading data from client sockets in a single call.
	MAX_BUFFER_SIZE = 4096

	// CLOSED indicates that a Worker has been shut down and is no longer processing connections.
	// In this state, the Worker has released its resources and cannot be restarted.
	CLOSED State = 0

	// INITIALIZED indicates that a Worker has been created but has not yet started processing connections.
	// In this state, the Worker has been allocated resources but is not yet actively listening.
	INITIALIZED State = 1

	// EXECUTING indicates that a Worker is actively running and processing connections.
	// In this state, the Worker is listening for incoming connections and handling client requests.
	EXECUTING State = 2

	// ErrClosed is returned when attempting to perform operations on a Worker that has already been closed.
	ErrWorkerClosed = errors.New("worker: operation failed because worker is already closed")
)

// New creates a worker instance with the given ID, port, and logger.
//
// This function initializes a worker and creates a wake-up pipe that will be used
// for interrupting the epoll_wait syscall during shutdown. The pipe's read end
// is set to non-blocking mode to avoid blocking when clearing wake-up events.
//
// Parameters:
//   - id: Unique identifier for the worker
//   - port: TCP port on which this worker will listen
//   - logger: Logger for worker events (or creates a default one if nil)
func New(id int, port uint, logger *log.Logger) (*Worker, error) {
	// Use provided logger or create a default one.
	if logger == nil {
		logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	}

	// Create the wake up pipe using the signature that returns fds in a slice.
	// This pipe will be used to signal listener goroutines that are blocked
	// in epoll_wait to wake up and check the cancellation context.

	// Slice to hold the read and write file descriptors.
	var p [2]int

	// syscall.Pipe will return the read end in p[0] and the write end in p[1].
	if err := syscall.Pipe(p[:]); err != nil {
		// If pipe creation fails, we cannot create the server.
		logger.Printf("Error creating wake up pipe: %v\n", err)
		return nil, fmt.Errorf("failed to create wake up pipe: %w", err)
	}

	rfd := p[0] // Read file descriptor is in index 0.
	wfd := p[1] // Write file descriptor is in index 1.

	// Set the read end of the pipe to non-blocking mode to avoid hanging
	// when reading during cleanup.
	if err := syscall.SetNonblock(rfd, true); err != nil {
		logger.Printf("Error setting wake up pipe read end non-blocking: %v\n", err)

		// Clean up the pipe fds before returning the error to prevent leaks
		logger.Printf("Closing wake up pipe fds %d (read) and %d (write)\n", rfd, wfd)

		if err := syscall.Close(rfd); err != nil {
			logger.Printf("Error closing wake up pipe read fd %d: %v\n", rfd, err)
		}
		if err := syscall.Close(wfd); err != nil {
			logger.Printf("Error closing wake up pipe write fd %d: %v\n", wfd, err)
		}

		return nil, fmt.Errorf("failed to set wake up pipe read end non-blocking: %w", err)
	}

	return &Worker{
		id:        id,
		port:      port,
		wakeUpRfd: rfd,
		wakeUpWfd: wfd,
		state:     INITIALIZED,
		log:       logger,
	}, nil
}

// State returns the current operational state of the worker.
func (w *Worker) State() State {
	return w.state
}

// Id returns the unique identifier of the worker.
func (w *Worker) Id() int {
	return w.id
}

// Start sets up the worker's listening socket, epoll instance, and runs the event loop.
//
// This method is the main entry point for a worker and is designed to be run as a
// goroutine. It creates a socket with SO_REUSEPORT to allow multiple workers to
// listen on the same port, sets up an epoll instance to monitor the socket and
// the wake-up pipe, and then enters the event loop.
//
// The event loop continues until a shutdown signal is received through the wake-up pipe.
func (w *Worker) Start() {
	// Set the worker state to EXECUTING and ensure it will be set to CLOSED when this function returns.
	w.state = EXECUTING
	defer func() {
		w.state = CLOSED
		w.log.Printf("Listener %d: Worker state changed to %s\n", w.id, w.state)
	}()

	w.log.Printf("Starting listener worker with id %d\n", w.id)

	// ============ SOCKET SETUP PHASE ============

	// 1. Create a listening socket for this goroutine.
	listenerFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		w.log.Printf("Listener %d: Error creating socket: %v\n", w.id, err)
		return
	}
	// Ensure the listening socket is closed when this goroutine exits.
	defer func() {
		w.log.Printf("Listener %d: Closing listener fd %d\n", w.id, listenerFd)
		if err := syscall.Close(listenerFd); err != nil {
			w.log.Printf("Listener %d: Error closing listener fd %d: %v\n", w.id, listenerFd, err)
		}
	}()

	// Set socket options for reusability and reusing the port.
	// SO_REUSEADDR allows binding to an address in TIME_WAIT state.
	if err := syscall.SetsockoptInt(listenerFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		w.log.Printf("Listener %d: Error setting SO_REUSEADDR: %v\n", w.id, err)
		return
	}

	// SO_REUSEPORT allows multiple sockets to bind to the same address and port,
	// which is essential for our multi-worker design to distribute connection load.
	if err := syscall.SetsockoptInt(listenerFd, syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1); err != nil {
		w.log.Printf("Listener %d: Error setting SO_REUSEPORT: %v\n", w.id, err)
		// SO_REUSEPORT might not be supported on all kernels or configurations.
		w.log.Printf("Listener %d: SO_REUSEPORT might not be supported or enabled.\n", w.id)
		return
	}

	// Bind the socket to the specified port on all interfaces (0.0.0.0).
	ip4 := net.ParseIP("0.0.0.0")
	addr := &syscall.SockaddrInet4{
		Port: int(w.port),
		Addr: [4]byte(ip4.To4()),
	}

	if err := syscall.Bind(listenerFd, addr); err != nil {
		w.log.Printf("Listener %d: Error binding socket: %v\n", w.id, err)
		return
	}

	// Start listening for incoming connections.
	// syscall.SOMAXCONN is the system default backlog queue size.
	if err := syscall.Listen(listenerFd, syscall.SOMAXCONN); err != nil {
		w.log.Printf("Listener %d: Error listening on socket: %v\n", w.id, err)
		return
	}

	w.log.Printf("Listener %d listening on port %d\n", w.id, w.port)

	// ============ EPOLL SETUP PHASE ============

	// 2. Create a dedicated epoll instance for this goroutine.
	epollFd, err := syscall.EpollCreate1(0) // 0 flag for default behavior
	if err != nil {
		w.log.Printf("Listener %d: Error creating epoll instance: %v\n", w.id, err)
		return
	}
	// Ensure the epoll instance file descriptor is closed when goroutine exits.
	defer func() {
		w.log.Printf("Listener %d: Closing epoll fd %d\n", w.id, epollFd)
		if err := syscall.Close(epollFd); err != nil {
			w.log.Printf("Listener %d: Error closing epoll fd %d: %v\n", w.id, epollFd, err)
		}
	}()

	// 3. Add the listener socket to this worker's epoll instance.
	// Monitor for EPOLLIN events (readiness for reading/accepting new connections).
	listenEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN, // Watch for incoming data (new connections for listen socket).
		Fd:     int32(listenerFd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, listenerFd, &listenEvent); err != nil {
		w.log.Printf("Listener %d: Error adding listening socket to epoll: %v\n", w.id, err)
		return
	}

	// Add the read end of the wake-up pipe to this epoll instance.
	// This allows us to interrupt the epoll_wait call during shutdown.
	wakeUpEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN, // Watch for readability (when a byte is written).
		Fd:     int32(w.wakeUpRfd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, w.wakeUpRfd, &wakeUpEvent); err != nil {
		w.log.Printf("Listener %d: Error adding wake up fd %d to epoll: %v\n", w.id, w.wakeUpRfd, err)
		return
	}

	// Slice to store events returned by epoll_wait for this goroutine.
	events := make([]syscall.EpollEvent, MAX_EVENTS)

	// ============ EVENT LOOP PHASE ============
	//
	// The event loop is the heart of the worker. It continuously waits for
	// and processes events on the sockets it's monitoring. The loop continues
	// until a shutdown signal is received via the wake-up pipe.
	//
	// For each iteration:
	// 1. epoll_wait blocks until events are ready or interrupted
	// 2. Process any wake-up pipe events (for shutdown)
	// 3. Process listener socket events (new connections)
	// 4. Process client socket events (data ready to read)
	for {
		// Wait for events. -1 means wait indefinitely.
		// The ready events are placed in the 'events' slice.
		n, err := syscall.EpollWait(epollFd, events, -1)
		if err != nil {
			// Handle EINTR (interrupted by signal) by continuing the loop.
			if err == syscall.EINTR {
				continue
			}
			w.log.Printf("Listener %d: Error in EpollWait: %v\n", w.id, err)
			continue
		}

		// Check if any of the ready events is on the wake-up pipe fd
		// This indicates a shutdown signal.
		isShuttingDown := slices.ContainsFunc(events[:n], func(e syscall.EpollEvent) bool {
			return e.Fd == int32(w.wakeUpRfd)
		})

		// Process each ready event for this goroutine.
		for i := range n {
			event := events[i]
			fd := int(event.Fd)

			// ============ Check if the event is on the wake up pipe FIRST ============
			if fd == w.wakeUpRfd {
				// Event on the wake up pipe - this means Stop was called.
				// Read the byte(s) from the pipe to clear the event from epoll.
				buf := make([]byte, 1)
				_, readErr := syscall.Read(w.wakeUpRfd, buf)

				// EAGAIN/EWOULDBLOCK are normal errors in non-blocking mode,
				// when there's no more data to read.
				if readErr != nil && readErr != syscall.EAGAIN && readErr != syscall.EWOULDBLOCK {
					w.log.Printf("Listener %d: Error reading from wake up pipe %d: %v\n", w.id, fd, readErr)
				}

				w.log.Printf("Listener %d: Woken up by pipe event on fd %d.\n", w.id, fd)

				// Close the wake up pipe file descriptors when stopping is complete.
				w.cleanupWakeUpPipeAndEpoll(epollFd)
				continue
			}

			// Handle the listening socket depending on shutdown state.
			if isShuttingDown && fd == listenerFd {
				// If shutting down, stop accepting new connections
				// by removing the listener fd from epoll.
				if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, listenerFd, nil); err != nil {
					w.log.Printf("Listener %d: Error removing listenerFd %d from epoll: %v\n", w.id, listenerFd, err)
				}
			} else if fd == listenerFd && !isShuttingDown {
				// This is a new connection event on the listening socket.

				// Accept the new connection.
				clientFd, _, err := syscall.Accept(listenerFd)
				if err != nil {
					// Accept errors can happen, e.g., if the connection is reset
					// before accept is called. Log and continue.
					w.log.Printf("Listener %d: Error accepting connection: %v\n", w.id, err)
					continue // Continue to the next event
				}

				// Set the accepted client socket to non-blocking mode.
				// This is essential for edge-triggered epoll.
				if err := syscall.SetNonblock(clientFd, true); err != nil {
					w.log.Printf("Listener %d: Error setting clientFd %d non-blocking: %v\n", w.id, clientFd, err)
					// Close the socket if setting non-blocking fails.
					if err := syscall.Close(clientFd); err != nil {
						w.log.Printf("Listener %d: Error closing clientFd %d after SetNonblock error: %v\n", w.id, clientFd, err)
					}
					continue
				}

				// Add the new client socket to this worker's epoll instance.
				// EPOLLIN: Monitor for read readiness (data available).
				// EPOLLET: Edge-triggered mode - only notifies on state transitions.
				// Which requires reading all available data at once.
				clientEvent := syscall.EpollEvent{
					Fd:     int32(clientFd),
					Events: syscall.EPOLLIN | syscall.EPOLLET,
				}
				if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, clientFd, &clientEvent); err != nil {
					w.log.Printf("Listener %d: Error adding clientFd %d to epoll: %v\n", w.id, clientFd, err)
					// Close if adding to epoll fails.
					if err := syscall.Close(clientFd); err != nil {
						w.log.Printf("Listener %d: Error closing clientFd %d after EpollCtl error: %v\n", w.id, clientFd, err)
					}
					continue
				}
				w.log.Printf("Listener %d: Accepted new connection, clientFd: %d\n", w.id, clientFd)
			} else {
				// Events on client sockets.
				if event.Events&syscall.EPOLLIN != 0 {
					// Data is ready to be read.
					w.handleClient(epollFd, fd)
					if isShuttingDown {
						// During shutdown, close client connections after handling any pending data.
						w.closeClient(epollFd, fd)
					}
				} else if event.Events&(syscall.EPOLLHUP|syscall.EPOLLERR) != 0 {
					// Client hung up or error - close the connection.
					w.log.Printf("Listener %d: Connection error or hung up on clientFd: %d\n", w.id, fd)
					w.closeClient(epollFd, fd)
				}
			}
		}

		// After processing all events in this batch, check if shutting down
		// and exit the event loop.
		if isShuttingDown {
			w.log.Printf("Listener %d: Shutdown flag set. Exiting event loop.\n", w.id)
			break
		}
	}
}

// Stop signals the worker to stop processing and begin cleanup.
//
// This method sends a wake-up signal through the pipe to interrupt
// the epoll_wait call in the event loop, causing the worker to begin
// its shutdown sequence.
//
// The behavior varies based on the current state of the worker:
//   - CLOSED: Returns ErrClosed without taking any action
//   - INITIALIZED: Directly closes the wake-up pipe file descriptors
//   - EXECUTING: Writes a wake-up byte to the pipe to interrupt the event loop
func (w *Worker) Stop() error {
	// If the worker is already closed, return ErrClosed.
	if w.state == CLOSED {
		return ErrWorkerClosed
	}

	// If the worker is only initialized but not executing yet,
	// we can directly close the wake-up pipe file descriptors
	if w.state == INITIALIZED {
		if err := w.closeWakeUpPipeFds(); err != nil {
			return err
		}
		w.log.Printf("Closed wake up pipe fds %d (read) and %d (write)\n", w.wakeUpRfd, w.wakeUpWfd)
		return nil
	}

	// Write a byte to the wake up pipe. This will make the read end of the pipe
	// readable, causing epoll_wait in each listener goroutine to return
	// with an event for the pipe's read file descriptor. This wakes up
	// the blocked goroutines.
	wakeUpByte := []byte{1}
	if _, err := syscall.Write(w.wakeUpWfd, wakeUpByte); err != nil {
		return fmt.Errorf("error writing to wake up pipe: %v", err)
	}
	return nil
}

// handleClient processes data from a ready client socket.
// It reads all available data in edge-triggered mode and echoes it back.
//
// In edge-triggered mode, we must read ALL available data until EAGAIN/EWOULDBLOCK
// is returned, as epoll will only notify us again when there's a state change
// (empty buffer -> data available).
func (w *Worker) handleClient(epollFd int, clientFd int) {
	buffer := make([]byte, MAX_BUFFER_SIZE)

	// In edge-triggered mode (EPOLLET), we continue the read loop to get all
	// pending data from the kernel buffer. The loop continues until syscall.Read
	// returns EAGAIN/EWOULDBLOCK or 0.
	for {
		nRead, err := syscall.Read(clientFd, buffer)
		if err != nil {
			// Check if the error is EAGAIN or EWOULDBLOCK, which means
			// we've read all available data for now in non-blocking mode.
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				return
			}

			// For other errors (e.g., connection reset by peer), close the connection.
			w.log.Printf("Listener %d: Error reading from clientFd %d: %v\n", w.id, clientFd, err)
			w.closeClient(epollFd, clientFd)
			return
		}

		// syscall.Read returns 0 when the client performs an orderly shutdown.
		if nRead == 0 {
			w.log.Printf("Listener %d: Client closed connection on clientFd: %d\n", w.id, clientFd)
			w.closeClient(epollFd, clientFd)
			return
		}

		// Data received, echo it back to the client.
		// Write the exact number of bytes read (buffer[:nRead]).
		nWritten, err := syscall.Write(clientFd, buffer[:nRead])
		if err != nil {
			w.log.Printf("Listener %d: Error writing to clientFd %d: %v\n", w.id, clientFd, err)
			w.closeClient(epollFd, clientFd)
			return
		}

		if nWritten != nRead {
			w.log.Printf(
				"Listener %d: Warning: Did not write all data back to clientFd %d. Wrote %d of %d bytes.\n",
				w.id, clientFd, nWritten, nRead,
			)
		}
	}
}

// closeClient cleans up a client connection by removing it from the epoll
// instance and closing the socket file descriptor.
func (w *Worker) closeClient(epollFd int, clientFd int) {
	// Remove the file descriptor from the epoll instance.
	// The last argument (event) can be nil for EPOLL_CTL_DEL.
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, clientFd, nil); err != nil {
		w.log.Printf("Listener %d: Error removing clientFd %d from epoll: %v\n", w.id, clientFd, err)
	}

	// Close the client socket file descriptor.
	if err := syscall.Close(clientFd); err != nil {
		w.log.Printf("Listener %d: Error closing clientFd %d: %v\n", w.id, clientFd, err)
		return
	}

	w.log.Printf("Listener %d: Closed connection on clientFd: %d\n", w.id, clientFd)
}

// cleanupWakeUpPipeAndEpoll removes the wake-up pipe's read file descriptor
// from the epoll instance and then closes both ends of the pipe.
func (w *Worker) cleanupWakeUpPipeAndEpoll(epollFd int) {
	// Remove the wake-up fd from epoll to stop receiving events for it.
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, w.wakeUpRfd, nil); err != nil {
		// Log the error but continue with closing the FDs.
		w.log.Printf("Listener %d: Error removing wakeUpRfd %d from epoll: %v\n", w.id, w.wakeUpRfd, err)
	}

	// Proceed to close the file descriptors for the pipe.
	if err := w.closeWakeUpPipeFds(); err != nil {
		w.log.Println(err)
	}
}

// closeWakeUpPipeFds closes the read and write file descriptors
// of the wake-up pipe. It returns an error if either close operation fails.
// If both fail, the returned error is a combination of both failure errors.
func (w *Worker) closeWakeUpPipeFds() (err error) {
	w.log.Printf("Closing wake up pipe fds %d (read) and %d (write)\n", w.wakeUpRfd, w.wakeUpWfd)

	var readErr error
	// Close the read end of the pipe.
	if e := syscall.Close(w.wakeUpRfd); e != nil {
		// Wrap the original error with context.
		readErr = fmt.Errorf("error closing wake up pipe read fd %d: %w", w.wakeUpRfd, e)
		w.log.Printf("%v", readErr)
	}

	var writeErr error
	// Close the write end of the pipe.
	if e := syscall.Close(w.wakeUpWfd); e != nil {
		// Wrap the original error with context.
		writeErr = fmt.Errorf("error closing wake up pipe write fd %d: %w", w.wakeUpWfd, e)
		w.log.Printf("%v", writeErr)
	}

	return errors.Join(readErr, writeErr)
}
