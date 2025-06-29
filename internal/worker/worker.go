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
	ErrWorkerClosed = errors.New("worker: operation failed because worker is already closed")
)

var (
	// MAX_EVENTS is the maximum number of events to retrieve in a single epoll_wait call.
	MAX_EVENTS = 1024

	// MAX_BUFFER_SIZE is the buffer size for reading data from client sockets in a single call.
	MAX_BUFFER_SIZE = 4096
)

const (
	CLOSED State = iota + 1
	INITIALIZED
	EXECUTING
)

// New creates a worker and creates a wake-up pipe that will be used
// for interrupting the epoll_wait syscall during shutdown.
func New(id int, port uint, logger *log.Logger) (*Worker, error) {
	if logger == nil {
		logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	}

	// Create the wake up pipe using the signature that returns fds in a slice.
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		logger.Printf("Error creating wake up pipe: %v\n", err)
		return nil, fmt.Errorf("failed to create wake up pipe: %w", err)
	}

	rfd := p[0]
	wfd := p[1]

	if err := syscall.SetNonblock(rfd, true); err != nil {
		logger.Printf("Error setting wake up pipe read end non-blocking: %v\n", err)

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
func (w *Worker) Start() {
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
	if err := syscall.SetsockoptInt(listenerFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		w.log.Printf("Listener %d: Error setting SO_REUSEADDR: %v\n", w.id, err)
		return
	}

	// SO_REUSEPORT allows multiple sockets to bind to the same address and port.
	if err := syscall.SetsockoptInt(listenerFd, syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1); err != nil {
		w.log.Printf("Listener %d: Error setting SO_REUSEPORT: %v\n", w.id, err)
		return
	}

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
	if err := syscall.Listen(listenerFd, syscall.SOMAXCONN); err != nil {
		w.log.Printf("Listener %d: Error listening on socket: %v\n", w.id, err)
		return
	}

	w.log.Printf("Listener %d listening on port %d\n", w.id, w.port)

	// ============ EPOLL SETUP PHASE ============

	// 2. Create a dedicated epoll instance for this goroutine.
	epollFd, err := syscall.EpollCreate1(0)
	if err != nil {
		w.log.Printf("Listener %d: Error creating epoll instance: %v\n", w.id, err)
		return
	}
	defer func() {
		w.log.Printf("Listener %d: Closing epoll fd %d\n", w.id, epollFd)
		if err := syscall.Close(epollFd); err != nil {
			w.log.Printf("Listener %d: Error closing epoll fd %d: %v\n", w.id, epollFd, err)
		}
	}()

	// 3. Add the listener socket to this worker's epoll instance.
	// Monitor for EPOLLIN events (readiness for reading/accepting new connections).
	listenEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN,
		Fd:     int32(listenerFd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, listenerFd, &listenEvent); err != nil {
		w.log.Printf("Listener %d: Error adding listening socket to epoll: %v\n", w.id, err)
		return
	}

	// Add the read end of the wake-up pipe to this epoll instance.
	wakeUpEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN,
		Fd:     int32(w.wakeUpRfd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, w.wakeUpRfd, &wakeUpEvent); err != nil {
		w.log.Printf("Listener %d: Error adding wake up fd %d to epoll: %v\n", w.id, w.wakeUpRfd, err)
		return
	}

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
		n, err := syscall.EpollWait(epollFd, events, -1)
		if err != nil {
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
				buf := make([]byte, 1)
				_, readErr := syscall.Read(w.wakeUpRfd, buf)

				// EAGAIN/EWOULDBLOCK are normal errors in non-blocking mode,
				// when there's no more data to read.
				if readErr != nil && readErr != syscall.EAGAIN && readErr != syscall.EWOULDBLOCK {
					w.log.Printf("Listener %d: Error reading from wake up pipe %d: %v\n", w.id, fd, readErr)
				}

				w.log.Printf("Listener %d: Woken up by pipe event on fd %d.\n", w.id, fd)
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
				// Accept the new connection.
				clientFd, _, err := syscall.Accept(listenerFd)
				if err != nil {
					// Accept errors can happen, e.g., if the connection is reset
					// before accept is called. Log and continue.
					w.log.Printf("Listener %d: Error accepting connection: %v\n", w.id, err)
					continue
				}

				// Set the accepted client socket to non-blocking mode.
				if err := syscall.SetNonblock(clientFd, true); err != nil {
					w.log.Printf("Listener %d: Error setting clientFd %d non-blocking: %v\n", w.id, clientFd, err)
					if err := syscall.Close(clientFd); err != nil {
						w.log.Printf("Listener %d: Error closing clientFd %d after SetNonblock error: %v\n", w.id, clientFd, err)
					}
					continue
				}

				// Add the new client socket to this worker's epoll instance.
				// EPOLLIN: Monitor for read readiness (data available).
				// EPOLLET: Edge-triggered mode - only notifies on state transitions.
				clientEvent := syscall.EpollEvent{
					Fd:     int32(clientFd),
					Events: syscall.EPOLLIN | syscall.EPOLLET,
				}
				if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, clientFd, &clientEvent); err != nil {
					w.log.Printf("Listener %d: Error adding clientFd %d to epoll: %v\n", w.id, clientFd, err)
					if err := syscall.Close(clientFd); err != nil {
						w.log.Printf("Listener %d: Error closing clientFd %d after EpollCtl error: %v\n", w.id, clientFd, err)
					}
					continue
				}
				w.log.Printf("Listener %d: Accepted new connection, clientFd: %d\n", w.id, clientFd)
			} else {
				if event.Events&syscall.EPOLLIN != 0 {
					w.handleClient(epollFd, fd)
					if isShuttingDown {
						w.closeClient(epollFd, fd)
					}
				} else if event.Events&(syscall.EPOLLHUP|syscall.EPOLLERR) != 0 {
					w.log.Printf("Listener %d: Connection error or hung up on clientFd: %d\n", w.id, fd)
					w.closeClient(epollFd, fd)
				}
			}
		}

		if isShuttingDown {
			w.log.Printf("Listener %d: Shutdown flag set. Exiting event loop.\n", w.id)
			break
		}
	}
}

// Stop signals the worker to stop processing and begin cleanup.
func (w *Worker) Stop() error {
	if w.state == CLOSED {
		return ErrWorkerClosed
	}

	if w.state == INITIALIZED {
		if err := w.closeWakeUpPipeFds(); err != nil {
			return err
		}
		w.log.Printf("Closed wake up pipe fds %d (read) and %d (write)\n", w.wakeUpRfd, w.wakeUpWfd)
		return nil
	}

	wakeUpByte := []byte{1}
	if _, err := syscall.Write(w.wakeUpWfd, wakeUpByte); err != nil {
		return fmt.Errorf("error writing to wake up pipe: %v", err)
	}

	return nil
}

// handleClient processes data from a ready client socket.
// It reads all available data in edge-triggered mode and echoes it back.
func (w *Worker) handleClient(epollFd int, clientFd int) {
	buffer := make([]byte, MAX_BUFFER_SIZE)

	// In edge-triggered mode (EPOLLET), we continue the read loop to get all
	// pending data from the kernel buffer. The loop continues until syscall.Read
	// returns EAGAIN/EWOULDBLOCK or 0.
	for {
		nRead, err := syscall.Read(clientFd, buffer)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				return
			}

			w.log.Printf("Listener %d: Error reading from clientFd %d: %v\n", w.id, clientFd, err)
			w.closeClient(epollFd, clientFd)
			return
		}

		if nRead == 0 {
			w.log.Printf("Listener %d: Client closed connection on clientFd: %d\n", w.id, clientFd)
			w.closeClient(epollFd, clientFd)
			return
		}

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
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, clientFd, nil); err != nil {
		w.log.Printf("Listener %d: Error removing clientFd %d from epoll: %v\n", w.id, clientFd, err)
	}

	if err := syscall.Close(clientFd); err != nil {
		w.log.Printf("Listener %d: Error closing clientFd %d: %v\n", w.id, clientFd, err)
		return
	}

	w.log.Printf("Listener %d: Closed connection on clientFd: %d\n", w.id, clientFd)
}

// cleanupWakeUpPipeAndEpoll removes the wake-up pipe's read file descriptor
// from the epoll instance and then closes both ends of the pipe.
func (w *Worker) cleanupWakeUpPipeAndEpoll(epollFd int) {
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, w.wakeUpRfd, nil); err != nil {
		w.log.Printf("Listener %d: Error removing wakeUpRfd %d from epoll: %v\n", w.id, w.wakeUpRfd, err)
	}

	if err := w.closeWakeUpPipeFds(); err != nil {
		w.log.Println(err)
	}
}

// closeWakeUpPipeFds closes the read and write file descriptors of the wake-up pipe.
func (w *Worker) closeWakeUpPipeFds() (err error) {
	w.log.Printf("Closing wake up pipe fds %d (read) and %d (write)\n", w.wakeUpRfd, w.wakeUpWfd)

	var readErr error
	if e := syscall.Close(w.wakeUpRfd); e != nil {
		readErr = fmt.Errorf("error closing wake up pipe read fd %d: %w", w.wakeUpRfd, e)
		w.log.Printf("%v", readErr)
	}

	var writeErr error
	if e := syscall.Close(w.wakeUpWfd); e != nil {
		writeErr = fmt.Errorf("error closing wake up pipe write fd %d: %w", w.wakeUpWfd, e)
		w.log.Printf("%v", writeErr)
	}

	return errors.Join(readErr, writeErr)
}
