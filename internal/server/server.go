package server

import (
	"context"
	"log"
	"sync"
)

type Server struct {
	port         uint
	maxListeners uint
	log          *log.Logger
	wg           sync.WaitGroup
	context      context.Context
	cancel       context.CancelFunc
}

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
			s.startListening(i + 1)
		}()
	}

	return nil
}

func (s *Server) Stop() error {
	s.cancel()
	s.wg.Wait()
	return nil
}

func (s *Server) startListening(worker int) {
	s.log.Printf("Starting worker with id %d\n", worker)

	select {
	case <-s.context.Done():
		return
	default:
		{
			// syscall.Socket(syscall.AF_INET)
		}
	}
}
