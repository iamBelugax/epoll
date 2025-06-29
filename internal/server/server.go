package server

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

// New creates a new Server instance.
func New(port, maxListeners uint) *Server {
	context, cancel := context.WithCancel(context.Background())
	return &Server{
		port:         port,
		log:          log.Default(),
		maxListeners: maxListeners,
		wg:           sync.WaitGroup{},
		context:      context,
		cancel:       cancel,
	}
}

func (s *Server) ListenAndServe() error {
	s.log.Printf("Server listening on port %d\n", s.port)

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
	s.log.Printf("Shutting down server...")
	s.cancel()
	s.wg.Wait()
	s.log.Printf("Sever shutdown complete")
	return nil
}

func (s *Server) startListener(worker int) {
	s.log.Printf("Starting worker with id %d\n", worker)

	listenerFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Printf("Listener goroutine on port %d: Error creating socket: %v\n", s.port, err)
		return
	}
	defer func() {
		if err := syscall.Close(listenerFd); err != nil {
			log.Printf("Listener goroutine on port %d: Error closing fd: %v\n", s.port, err)
			return
		}
	}()

	for {
		select {
		case <-s.context.Done():
			s.log.Printf("Closing worker with id %d\n", worker)
			return
		default:
			continue
		}
	}
}
