package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/iamNilotpal/epoll/internal/epoll"
)

var (
	// DEFAULT_PORT is the default port the server will listen on if not specified.
	DEFAULT_PORT uint = 8080

	// DEFAULT_MAX_LISTENERS is the default number of listener workers to start.
	// It uses the system's CPU count as a sensible default to maximize parallelism.
	DEFAULT_MAX_LISTENERS = uint(runtime.NumCPU())
)

func main() {
	port, maxListeners := initializeVariables()

	svr, err := epoll.NewServer(port, maxListeners)
	if err != nil {
		log.Fatalln("create server error", err)
	}
	defer func() {
		if err := svr.Stop(); err != nil {
			log.Fatalln("server stopping error", err)
		}
	}()

	if err := svr.ListenAndServe(); err != nil {
		log.Fatalln("server startup error", err)
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-stopChan
	log.Printf("Received signal %s, initiating shutdown...\n", sig)
}

func initializeVariables() (uint, uint) {
	port := flag.Uint("port", DEFAULT_PORT, "Port for the server will listen on")
	maxListeners := flag.Uint("maxListeners", DEFAULT_MAX_LISTENERS, "Number of listeners to start")
	flag.Parse()

	if *port == 0 {
		*port = DEFAULT_PORT
	}

	if *maxListeners == 0 {
		*maxListeners = DEFAULT_MAX_LISTENERS
	}

	return *port, *maxListeners
}
