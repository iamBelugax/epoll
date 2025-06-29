package epoll

import (
	"log"
	"sync"

	"github.com/iamBelugaa/epoll-test/internal/worker"
)

type Server struct {
	port         uint
	maxListeners uint
	log          *log.Logger
	wg           sync.WaitGroup
	workers      []*worker.Worker
}

type WorkerStatus struct {
	Id     int
	Status string
}
