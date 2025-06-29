package epoll

import (
	"log"
	"os"
	"sync"

	"github.com/iamNilotpal/epoll/internal/worker"
)

// NewServer creates a server instance along with its worker instances,
// each responsible for accepting and handling connections.
func NewServer(port, maxListeners uint) (*Server, error) {
	workers := make([]*worker.Worker, maxListeners)
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)

	for i := range maxListeners {
		worker, err := worker.New(int(i+1), port, logger)
		if err != nil {
			for _, worker := range workers {
				if worker != nil {
					if err := worker.Stop(); err != nil {
						logger.Println(err)
					}
				}
			}
			return nil, err
		}
		workers[i] = worker
	}

	return &Server{
		port:         port,
		log:          logger,
		workers:      workers,
		maxListeners: maxListeners,
		wg:           sync.WaitGroup{},
	}, nil
}

// ListenAndServe starts all worker goroutines and begins accepting connections.
func (s *Server) ListenAndServe() error {
	s.log.Printf("Server listening on port %d with %d listeners\n", s.port, s.maxListeners)

	s.wg.Add(int(s.maxListeners))
	for _, worker := range s.workers {
		go func() {
			defer s.wg.Done()
			worker.Start()
		}()
	}

	return nil
}

// Stop gracefully shuts down the server by stopping all worker goroutines.
func (s *Server) Stop() error {
	s.log.Printf("Initiated Server Shutdown...")

	for _, worker := range s.workers {
		if err := worker.Stop(); err != nil {
			return err
		}
	}

	s.wg.Wait()
	s.log.Printf("Server Shutdown Completed")

	return nil
}

// State returns the current operational status of all worker goroutines.
func (s *Server) State() []WorkerStatus {
	states := make([]WorkerStatus, s.maxListeners)
	for i, worker := range s.workers {
		states[i] = WorkerStatus{Id: worker.Id(), Status: worker.State().String()}
	}
	return states
}
