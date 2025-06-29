package worker

import "log"

// State represents the current operational state of a Worker.
type State uint8

// Worker represents an individual listener process that handles its own connections.
type Worker struct {
	log       *log.Logger
	id        int   // A unique identifier for the worker.
	port      uint  // The TCP port on which this worker listens.
	wakeUpRfd int   // The read end of the wake-up pipe, used to interrupt epoll_wait.
	wakeUpWfd int   // The write end of the wake-up pipe, used to signal worker shutdown.
	state     State // Current operational state (INITIALIZED, EXECUTING, or CLOSED).
}

// String returns the string representation of the State enum.
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
