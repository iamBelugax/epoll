package epoll

import (
	"log"
	"os"
	"sync"

	"github.com/iamNilotpal/epoll/internal/worker"
)

// Server represents an epoll-based TCP server that leverages multiple worker goroutines
// to handle connections. Each worker uses Linux's epoll mechanism to efficiently
// monitor multiple file descriptors with a single thread.
//
// The design pattern follows a leader/follower model where multiple workers
// listen on the same port using SO_REUSEPORT, allowing the kernel to distribute
// incoming connections across listeners for better performance and load balancing.
type Server struct {
	port         uint             // TCP port on which all listeners will bind.
	maxListeners uint             // The number of concurrent listener workers to create.
	log          *log.Logger      // Logger for server events and errors.
	wg           sync.WaitGroup   // WaitGroup to track and wait for all listener goroutines.
	workers      []*worker.Worker // Collection of worker instances, each running in its own goroutine.
}

// NewServer initializes a new Server with the given configuration parameters.
//
// This factory function creates a server instance along with its worker instances,
// each responsible for accepting and handling connections. The workers are created
// but not started until ListenAndServe is called.
//
// Parameters:
//   - port: TCP port on which all listeners will bind
//   - maxListeners: Number of concurrent listener workers to create
//
// Returns:
//   - Pointer to initialized Server and any error encountered during setup
func NewServer(port, maxListeners uint) (*Server, error) {
	// Initialize the slice of workers with the capacity of maxListeners
	workers := make([]*worker.Worker, maxListeners)
	// Create a standard logger that writes to stdout with time and file information
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)

	// Create each worker with its own ID but sharing the same port
	for i := range maxListeners {
		worker, err := worker.New(int(i+1), port, logger)
		if err != nil {
			// If any worker creation fails, return error immediately.
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

	// Return the fully initialized server ready to be started.
	return &Server{
		port:         port,
		log:          logger,
		workers:      workers,
		maxListeners: maxListeners,
		wg:           sync.WaitGroup{},
	}, nil
}

// ListenAndServe starts all worker goroutines and begins accepting connections.
//
// This method launches each worker in its own goroutine, leveraging Go's
// concurrency model. Each worker creates its own listening socket on the same
// port (enabled by SO_REUSEPORT) and epoll instance.
//
// The server continues running in the background until Stop is called.
func (s *Server) ListenAndServe() error {
	s.log.Printf("Server listening on port %d with %d listeners\n", s.port, s.maxListeners)

	// Add each worker to the WaitGroup to track all goroutines.
	s.wg.Add(int(s.maxListeners))

	// Launch each worker in its own goroutine.
	for _, worker := range s.workers {
		go func() {
			defer s.wg.Done()
			worker.Start()
		}()
	}

	return nil
}

// Stop gracefully shuts down the server by stopping all worker goroutines.
//
// This method signals each worker to stop processing, then waits for all goroutines
// to complete their cleanup before returning. The shutdown is coordinated through
// wake-up pipes in each worker that interrupt the epoll_wait syscall.
//
// The WaitGroup ensures we don't return until all workers have completely shut down.
//
// Returns:
//   - Error if any issue occurs during shutdown
func (s *Server) Stop() error {
	s.log.Printf("Initiated Server Shutdown...")

	// Signal each worker to stop processing and begin cleanup.
	for _, worker := range s.workers {
		if err := worker.Stop(); err != nil {
			return err
		}
	}

	// Wait for all goroutines in the WaitGroup to finish. They should exit
	// after being woken up by the pipe and checking the context.
	s.wg.Wait()

	s.log.Printf("Server Shutdown Completed")
	return nil
}
