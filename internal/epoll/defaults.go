package epoll

var (
	// Maximum number of events to retrieve from epoll_wait per goroutine.
	MAX_EVENTS = 1024
	// Buffer size for reading data from client sockets.
	MAX_BUFFER_SIZE = 4096
	// SO_MAX_CONN is kernel parameter in Linux that determines the maximum number of connections that can be queued in the TCP/IP stack backlog per socket.
	SO_MAX_CONN = 1024
)
