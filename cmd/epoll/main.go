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
	// Parse command-line flags and initialize variables.
	port, maxListeners := initializeVariables()

	// Create a new Server instance with the configured parameters.
	svr, err := epoll.NewServer(port, maxListeners)
	if err != nil {
		log.Fatalln("create server error", err)
	}
	// Ensure the server is stopped gracefully when main exits.
	defer func() {
		// Stop the server gracefully.
		if err := svr.Stop(); err != nil {
			log.Fatalln("server stopping error", err)
		}
	}()

	// Start the server. This launches the listener goroutines.
	if err := svr.ListenAndServe(); err != nil {
		log.Fatalln("server startup error", err)
	}

	// Set up a channel to listen for OS signals for graceful shutdown.
	stopChan := make(chan os.Signal, 1)
	// Register to receive SIGINT (Ctrl+C) and SIGTERM signals.
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// Block until a stop signal is received.
	sig := <-stopChan
	log.Printf("Received signal %s, initiating shutdown...\n", sig)

	// After receiving the signal, the deferred svr.Stop() will be called
	// automatically when main exits.
}

// initializeVariables parses command-line flags and returns the configured
// port and maximum number of listeners to use.
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
