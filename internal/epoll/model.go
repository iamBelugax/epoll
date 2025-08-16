package epoll

import (
	"log"
	"sync"

	"github.com/iamBelugax/epoll/internal/worker"
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
