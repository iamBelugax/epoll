package worker

import "log"

type State uint8

type Worker struct {
	id        int
	wakeUpRfd int
	wakeUpWfd int
	port      uint
	state     State
	log       *log.Logger
}

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
