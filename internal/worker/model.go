package worker

import "log"

// State represents the current operational state of a Worker.
//
// It tracks the lifecycle of a Worker through various phases from initialization
// to execution and finally closure, allowing for proper state management and
// validation of operations based on the current state.
type State uint8

// Worker represents an individual listener process that handles its own connections.
//
// Each Worker runs in its own goroutine with a dedicated epoll instance, allowing
// it to independently monitor and handle multiple file descriptors efficiently.
// Workers share the same port using SO_REUSEPORT socket option, which enables
// the kernel to distribute incoming connections across workers.
//
// The internal wake-up pipe mechanism allows for graceful interruption of the
// blocking epoll_wait call during shutdown.
type Worker struct {
	id        int         // A unique identifier for the worker.
	port      uint        // The TCP port on which this worker listens.
	wakeUpRfd int         // The read end of the wake-up pipe, used to interrupt epoll_wait.
	wakeUpWfd int         // The write end of the wake-up pipe, used to signal worker shutdown.
	state     State       // Current operational state (INITIALIZED, EXECUTING, or CLOSED).
	log       *log.Logger // Logger for worker events and errors.
}

// String returns the string representation of the State enum.
// It provides human-readable names for known state constants,
// and returns "UNKNOWN" for any unrecognized state value.
func (s State) String() string {
	switch s {
	case CLOSED:
		return "CLOSED"
	case EXECUTING:
		return "EXECUTING"
	case INITIALIZED:
		return "INITIALIZED"
	default:
		return "UNKNOWN"
	}
}
