package epoll

import (
	"log"
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

// WorkerStatus represents the current status of an individual worker goroutine.
type WorkerStatus struct {
	Id     int    // Unique ID for the worker (starting from 1).
	Status string // Current operational state (e.g., "INITIALIZED", "EXECUTING", "CLOSED").
}
