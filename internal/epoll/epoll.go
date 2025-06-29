package epoll

import (
	"context"
	"log"
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
}
