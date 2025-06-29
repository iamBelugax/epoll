package epoll

import (
	"context"
	"log"
	"net"
	"sync"
	"syscall"
)

// Server holds the server configuration and manages listener goroutines.
type Server struct {
	port         uint
	maxListeners uint
	log          *log.Logger
	wg           sync.WaitGroup
	context      context.Context
	cancel       context.CancelFunc
}

// NewServer creates a new Server instance.
func NewServer(port, maxListeners uint) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		port:         port,
		log:          log.Default(),
		maxListeners: maxListeners,
		wg:           sync.WaitGroup{},
		context:      ctx,
		cancel:       cancel,
	}, nil
}

// ListenAndServe returns after launching listeners.
// The server continues to run in the background via the listener goroutines.
func (s *Server) ListenAndServe() error {
	s.log.Printf("Server listening on port %d with %d listeners\n", s.port, s.maxListeners)

	total := int(s.maxListeners)
	s.wg.Add(total)

	for i := range total {
		go func() {
			defer s.wg.Done()
			s.startListener(i + 1)
		}()
	}

	return nil
}

// Stop signals the listener goroutines to stop and waits for them to finish.
func (s *Server) Stop() error {
	s.log.Printf("Server Shutdown Initiated")
	s.cancel() // Signal cancellation by calling the cancel function.

	// Wait for all goroutines in the WaitGroup to finish. They should exit
	// after being woken up by the pipe and checking the context.
	s.wg.Wait()

	s.log.Printf("Server Shutdown Complete")
	return nil
}

// startListener sets up a listener socket, its epoll instance, and runs the event loop.
// This function is designed to be run as a goroutine.
func (s *Server) startListener(id int) {
	s.log.Printf("Starting listener worker with id %d\n", id)

	// 1. Create a listening socket for this goroutine.
	listenFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		s.log.Printf("Listener %d: Error creating socket: %v\n", id, err)
		return
	}
	// Ensure the listening socket is closed when this goroutine exits.
	defer func() {
		s.log.Printf("Listener %d: Closing listener fd %d\n", id, listenFd)
		if err := syscall.Close(listenFd); err != nil {
			s.log.Printf("Listener %d: Error closing listener fd %d: %v\n", id, listenFd, err)
		}
	}()

	// Set socket options for reusability and reusing the port.
	// SO_REUSEADDR allows binding to an address in TIME_WAIT state.
	if err := syscall.SetsockoptInt(listenFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		s.log.Printf("Listener %d: Error setting SO_REUSEADDR: %v\n", id, err)
		return
	}

	// SO_REUSEPORT allows multiple sockets to bind to the same address and port.
	if err := syscall.SetsockoptInt(listenFd, syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1); err != nil {
		s.log.Printf("Listener %d: Error setting SO_REUSEPORT: %v\n", id, err)
		// SO_REUSEPORT might not be supported on all kernels or configurations.
		s.log.Printf("Listener %d: SO_REUSEPORT might not be supported or enabled.\n", id)
		return
	}

	// Bind the socket to the specified port on all interfaces (0.0.0.0).
	ip4 := net.ParseIP("0.0.0.0")
	addr := &syscall.SockaddrInet4{
		Port: int(s.port),
		Addr: [4]byte(ip4.To4()),
	}

	if err := syscall.Bind(listenFd, addr); err != nil {
		s.log.Printf("Listener %d: Error binding socket: %v\n", id, err)
		return
	}

	// Start listening for incoming connections.
	// syscall.SOMAXCONN is the system default backlog queue size.
	if err := syscall.Listen(listenFd, syscall.SOMAXCONN); err != nil {
		s.log.Printf("Listener %d: Error listening on socket: %v\n", id, err)
		return
	}

	s.log.Printf("Listener %d listening on port %d\n", id, s.port)

	// 2. Create a dedicated epoll instance for this goroutine.
	epollFd, err := syscall.EpollCreate1(0) // 0 flag for default behavior
	if err != nil {
		s.log.Printf("Listener %d: Error creating epoll instance: %v\n", id, err)
		return
	}
	// Ensure the epoll instance file descriptor is closed.
	defer func() {
		s.log.Printf("Listener %d: Closing epoll fd %d\n", id, epollFd)
		if err := syscall.Close(epollFd); err != nil {
			s.log.Printf("Listener %d: Error closing epoll fd %d: %v\n", id, epollFd, err)
		}
	}()

	// 3. Add this listener socket to its dedicated epoll instance.
	// Watch for incoming data (new connections for listen socket)
	listenEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN, // Watch for incoming data (new connections for listen socket)
		Fd:     int32(listenFd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, listenFd, &listenEvent); err != nil {
		s.log.Printf("Listener %d: Error adding listening socket to epoll: %v\n", id, err)
		return
	}

	// Slice to store events returned by epoll_wait for this goroutine.
	events := make([]syscall.EpollEvent, MAX_EVENTS)

	// 4. Event loop for this listener goroutine.
	// This loop continuously waits for and processes events from this goroutine's epoll instance.
	for {
		// Select statement to check for both epoll events and the stop signal.
		select {
		// Received stop signal, exit the goroutine.
		case <-s.context.Done():
			s.log.Printf("Listener %d: Received stop signal. Shutting down.\n", id)
			return
		default:
			// Continue with epoll_wait if no stop signal.
		}

		// Wait for events. -1 means wait indefinitely.
		// The ready events are placed in the 'events' slice.
		n, err := syscall.EpollWait(epollFd, events, -1)
		if err != nil {
			// Handle EINTR (interrupted by signal) by continuing the loop.
			if err == syscall.EINTR {
				continue
			}
			s.log.Printf("Listener %d: Error in EpollWait: %v\n", id, err)
			continue
		}

		// Process each ready event for this goroutine.
		for i := range n {
			event := events[i]
			fd := int(event.Fd)

			// Check if the event is on the listening socket.
			if fd == listenFd {
				// Event on the listening socket means a new connection is ready.
				// Accept the connection. This is handled by the specific goroutine
				// whose listener received the connection via SO_REUSEPORT.
				clientFd, _, err := syscall.Accept(listenFd)
				if err != nil {
					// Accept errors can happen, e.g., if the connection is reset
					// before accept is called. Log and continue.
					s.log.Printf("Listener %d: Error accepting connection: %v\n", id, err)
					continue
				}

				// Set the accepted client socket to non-blocking mode.
				if err := syscall.SetNonblock(clientFd, true); err != nil {
					s.log.Printf("Listener %d: Error setting clientFd %d non-blocking: %v\n", id, clientFd, err)
					// Close the socket if setting non-blocking fails.
					if err := syscall.Close(clientFd); err != nil {
						s.log.Printf("Listener %d: Error closing clientFd %d after SetNonblock error: %v\n", id, clientFd, err)
					}
					continue
				}

				// Add the new client socket to *this* goroutine's epoll instance.
				// We are interested in EPOLLIN (read readiness) and use EPOLLET (edge-triggered).
				clientEvent := syscall.EpollEvent{
					Fd:     int32(clientFd),
					Events: syscall.EPOLLIN | syscall.EPOLLET, // Watch for read readiness (Edge-Triggered)
				}
				if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, clientFd, &clientEvent); err != nil {
					s.log.Printf("Listener %d: Error adding clientFd %d to epoll: %v\n", id, clientFd, err)
					// Close if adding to epoll fails.
					if err := syscall.Close(clientFd); err != nil {
						s.log.Printf("Listener %d: Error closing clientFd %d after EpollCtl error: %v\n", id, clientFd, err)
					}
					continue
				}
				s.log.Printf("Listener %d: Accepted new connection, clientFd: %d\n", id, clientFd)
			} else {
				// Data is ready to be read. Call the handler function.
				if event.Events&syscall.EPOLLIN != 0 {
					s.handleClient(epollFd, fd, id)
				} else if event.Events&(syscall.EPOLLHUP|syscall.EPOLLERR) != 0 {
					// Client hung up or error.
					s.log.Printf("Listener %d: Connection error or hung up on clientFd: %d\n", id, fd)
					s.closeClient(epollFd, fd, id)
				}
			}
		}
	}
}

// handleClient processes data from a ready client socket.
// It reads all available data in edge-triggered mode and echoes it back.
func (s *Server) handleClient(epollFd int, clientFd int, listenerId int) {
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
			s.log.Printf("Listener %d: Error reading from clientFd %d: %v\n", listenerId, clientFd, err)
			s.closeClient(epollFd, clientFd, listenerId)
			return
		}

		// syscall.Read returns 0 when the client performs an orderly shutdown.
		if nRead == 0 {
			s.log.Printf("Listener %d: Client closed connection on clientFd: %d\n", listenerId, clientFd)
			s.closeClient(epollFd, clientFd, listenerId)
			return
		}

		// Data received, echo it back to the client.
		nWritten, err := syscall.Write(clientFd, buffer[:nRead])
		if err != nil {
			s.log.Printf("Listener %d: Error writing to clientFd %d: %v\n", listenerId, clientFd, err)
			s.closeClient(epollFd, clientFd, listenerId)
			return
		}

		if nWritten != nRead {
			s.log.Printf(
				"Listener %d: Warning: Did not write all data back to clientFd %d. Wrote %d of %d bytes.\n",
				listenerId, clientFd, nWritten, nRead,
			)
		}
	}
}

// closeClient cleans up a client connection by closing the socket
// and removing it from the epoll instance.
func (s *Server) closeClient(epollFd int, clientFd int, listenerId int) {
	// Remove the file descriptor from the epoll instance.
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, clientFd, nil); err != nil {
		s.log.Printf("Listener %d: Error removing clientFd %d from epoll: %v\n", listenerId, clientFd, err)
	}

	// Close the client socket file descriptor.
	if err := syscall.Close(clientFd); err != nil {
		s.log.Printf("Listener %d: Error closing clientFd %d: %v\n", listenerId, clientFd, err)
		return
	}

	s.log.Printf("Listener %d: Closed connection on clientFd: %d\n", listenerId, clientFd)
}
