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
