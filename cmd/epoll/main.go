package main

import (
	"flag"
	"log"
	"runtime"

	"github.com/iamNilotpal/epoll/internal/server"
)

var (
	DEFAULT_PORT          uint = 8080
	DEFAULT_MAX_LISTENERS uint = uint(runtime.NumCPU())
)

func main() {
	port := flag.Uint("port", DEFAULT_PORT, "Server starting port")
	maxListeners := flag.Uint("maxListeners", DEFAULT_MAX_LISTENERS, "Max TCP listeners")
	flag.Parse()

	svr := server.New(*port, *maxListeners)
	defer func() {
		if err := svr.Stop(); err != nil {
			log.Fatalln("server stopping error", err)
		}
	}()

	if err := svr.ListenAndServe(); err != nil {
		log.Fatalln("server startup error", err)
	}

}
