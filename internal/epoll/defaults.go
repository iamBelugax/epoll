package epoll

var (
	// Maximum number of events to retrieve from epoll_wait per goroutine.
	MAX_EVENTS = 1024
	// Buffer size for reading data from client sockets.
	MAX_BUFFER_SIZE = 4096
)
