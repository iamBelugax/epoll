package worker

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"slices"
	"syscall"
)

var (
	MAX_EVENTS      = 1024
	MAX_BUFFER_SIZE = 4096
	ErrWorkerClosed = errors.New("worker: operation failed because worker is already closed")
)

const (
	CLOSED State = iota + 1
	INITIALIZED
	EXECUTING
)

func New(id int, port uint, logger *log.Logger) (*Worker, error) {
	if logger == nil {
		logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	}

	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		logger.Printf("Error creating wake up pipe: %v\n", err)
		return nil, fmt.Errorf("failed to create wake up pipe: %w", err)
	}

	rfd := p[0]
	wfd := p[1]

	if err := syscall.SetNonblock(rfd, true); err != nil {
		logger.Printf("Error setting wake up pipe read end non-blocking: %v\n", err)

		if err := syscall.Close(rfd); err != nil {
			logger.Printf("Error closing wake up pipe read fd %d: %v\n", rfd, err)
		}
		if err := syscall.Close(wfd); err != nil {
			logger.Printf("Error closing wake up pipe write fd %d: %v\n", wfd, err)
		}

		return nil, fmt.Errorf("failed to set wake up pipe read end non-blocking: %w", err)
	}

	return &Worker{
		id:        id,
		port:      port,
		wakeUpRfd: rfd,
		wakeUpWfd: wfd,
		state:     INITIALIZED,
		log:       logger,
	}, nil
}

func (w *Worker) State() State {
	return w.state
}

func (w *Worker) Id() int {
	return w.id
}

func (w *Worker) Start() {
	w.state = EXECUTING
	defer func() {
		w.state = CLOSED
		w.log.Printf("Listener %d: Worker state changed to %s\n", w.id, w.state)
	}()

	w.log.Printf("Starting listener worker with id %d\n", w.id)

	listenerFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		w.log.Printf("Listener %d: Error creating socket: %v\n", w.id, err)
		return
	}

	defer func() {
		w.log.Printf("Listener %d: Closing listener fd %d\n", w.id, listenerFd)
		if err := syscall.Close(listenerFd); err != nil {
			w.log.Printf("Listener %d: Error closing listener fd %d: %v\n", w.id, listenerFd, err)
		}
	}()

	if err := syscall.SetsockoptInt(listenerFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		w.log.Printf("Listener %d: Error setting SO_REUSEADDR: %v\n", w.id, err)
		return
	}

	if err := syscall.SetsockoptInt(listenerFd, syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1); err != nil {
		w.log.Printf("Listener %d: Error setting SO_REUSEPORT: %v\n", w.id, err)
		return
	}

	ip4 := net.ParseIP("0.0.0.0")
	addr := &syscall.SockaddrInet4{
		Port: int(w.port),
		Addr: [4]byte(ip4.To4()),
	}

	if err := syscall.Bind(listenerFd, addr); err != nil {
		w.log.Printf("Listener %d: Error binding socket: %v\n", w.id, err)
		return
	}

	if err := syscall.Listen(listenerFd, syscall.SOMAXCONN); err != nil {
		w.log.Printf("Listener %d: Error listening on socket: %v\n", w.id, err)
		return
	}

	w.log.Printf("Listener %d listening on port %d\n", w.id, w.port)

	epollFd, err := syscall.EpollCreate1(0)
	if err != nil {
		w.log.Printf("Listener %d: Error creating epoll instance: %v\n", w.id, err)
		return
	}
	defer func() {
		w.log.Printf("Listener %d: Closing epoll fd %d\n", w.id, epollFd)
		if err := syscall.Close(epollFd); err != nil {
			w.log.Printf("Listener %d: Error closing epoll fd %d: %v\n", w.id, epollFd, err)
		}
	}()

	listenEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN,
		Fd:     int32(listenerFd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, listenerFd, &listenEvent); err != nil {
		w.log.Printf("Listener %d: Error adding listening socket to epoll: %v\n", w.id, err)
		return
	}

	wakeUpEvent := syscall.EpollEvent{
		Events: syscall.EPOLLIN,
		Fd:     int32(w.wakeUpRfd),
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, w.wakeUpRfd, &wakeUpEvent); err != nil {
		w.log.Printf("Listener %d: Error adding wake up fd %d to epoll: %v\n", w.id, w.wakeUpRfd, err)
		return
	}

	events := make([]syscall.EpollEvent, MAX_EVENTS)

	for {
		n, err := syscall.EpollWait(epollFd, events, -1)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			w.log.Printf("Listener %d: Error in EpollWait: %v\n", w.id, err)
			continue
		}

		isShuttingDown := slices.ContainsFunc(events[:n], func(e syscall.EpollEvent) bool {
			return e.Fd == int32(w.wakeUpRfd)
		})

		for i := range n {
			event := events[i]
			fd := int(event.Fd)

			if fd == w.wakeUpRfd {
				buf := make([]byte, 1)
				_, readErr := syscall.Read(w.wakeUpRfd, buf)

				if readErr != nil && readErr != syscall.EAGAIN && readErr != syscall.EWOULDBLOCK {
					w.log.Printf("Listener %d: Error reading from wake up pipe %d: %v\n", w.id, fd, readErr)
				}

				w.log.Printf("Listener %d: Woken up by pipe event on fd %d.\n", w.id, fd)
				w.cleanupWakeUpPipeAndEpoll(epollFd)
				continue
			}

			if isShuttingDown && fd == listenerFd {
				if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, listenerFd, nil); err != nil {
					w.log.Printf("Listener %d: Error removing listenerFd %d from epoll: %v\n", w.id, listenerFd, err)
				}
			} else if fd == listenerFd && !isShuttingDown {
				clientFd, _, err := syscall.Accept(listenerFd)
				if err != nil {
					w.log.Printf("Listener %d: Error accepting connection: %v\n", w.id, err)
					continue
				}

				if err := syscall.SetNonblock(clientFd, true); err != nil {
					w.log.Printf("Listener %d: Error setting clientFd %d non-blocking: %v\n", w.id, clientFd, err)
					if err := syscall.Close(clientFd); err != nil {
						w.log.Printf("Listener %d: Error closing clientFd %d after SetNonblock error: %v\n", w.id, clientFd, err)
					}
					continue
				}

				clientEvent := syscall.EpollEvent{
					Fd:     int32(clientFd),
					Events: syscall.EPOLLIN | syscall.EPOLLET,
				}
				if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, clientFd, &clientEvent); err != nil {
					w.log.Printf("Listener %d: Error adding clientFd %d to epoll: %v\n", w.id, clientFd, err)
					if err := syscall.Close(clientFd); err != nil {
						w.log.Printf("Listener %d: Error closing clientFd %d after EpollCtl error: %v\n", w.id, clientFd, err)
					}
					continue
				}
				w.log.Printf("Listener %d: Accepted new connection, clientFd: %d\n", w.id, clientFd)
			} else {
				if event.Events&syscall.EPOLLIN != 0 {
					w.handleClient(epollFd, fd)
					if isShuttingDown {
						w.closeClient(epollFd, fd)
					}
				} else if event.Events&(syscall.EPOLLHUP|syscall.EPOLLERR) != 0 {
					w.log.Printf("Listener %d: Connection error or hung up on clientFd: %d\n", w.id, fd)
					w.closeClient(epollFd, fd)
				}
			}
		}

		if isShuttingDown {
			w.log.Printf("Listener %d: Shutdown flag set. Exiting event loop.\n", w.id)
			break
		}
	}
}

func (w *Worker) Stop() error {
	if w.state == CLOSED {
		return ErrWorkerClosed
	}

	if w.state == INITIALIZED {
		if err := w.closeWakeUpPipeFds(); err != nil {
			return err
		}
		w.log.Printf("Closed wake up pipe fds %d (read) and %d (write)\n", w.wakeUpRfd, w.wakeUpWfd)
		return nil
	}

	wakeUpByte := []byte{1}
	if _, err := syscall.Write(w.wakeUpWfd, wakeUpByte); err != nil {
		return fmt.Errorf("error writing to wake up pipe: %v", err)
	}

	return nil
}

func (w *Worker) handleClient(epollFd int, clientFd int) {
	buffer := make([]byte, MAX_BUFFER_SIZE)

	for {
		nRead, err := syscall.Read(clientFd, buffer)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				return
			}

			w.log.Printf("Listener %d: Error reading from clientFd %d: %v\n", w.id, clientFd, err)
			w.closeClient(epollFd, clientFd)
			return
		}

		if nRead == 0 {
			w.log.Printf("Listener %d: Client closed connection on clientFd: %d\n", w.id, clientFd)
			w.closeClient(epollFd, clientFd)
			return
		}

		nWritten, err := syscall.Write(clientFd, buffer[:nRead])
		if err != nil {
			w.log.Printf("Listener %d: Error writing to clientFd %d: %v\n", w.id, clientFd, err)
			w.closeClient(epollFd, clientFd)
			return
		}

		if nWritten != nRead {
			w.log.Printf(
				"Listener %d: Warning: Did not write all data back to clientFd %d. Wrote %d of %d bytes.\n",
				w.id, clientFd, nWritten, nRead,
			)
		}
	}
}

func (w *Worker) closeClient(epollFd int, clientFd int) {
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, clientFd, nil); err != nil {
		w.log.Printf("Listener %d: Error removing clientFd %d from epoll: %v\n", w.id, clientFd, err)
	}

	if err := syscall.Close(clientFd); err != nil {
		w.log.Printf("Listener %d: Error closing clientFd %d: %v\n", w.id, clientFd, err)
		return
	}

	w.log.Printf("Listener %d: Closed connection on clientFd: %d\n", w.id, clientFd)
}

func (w *Worker) cleanupWakeUpPipeAndEpoll(epollFd int) {
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_DEL, w.wakeUpRfd, nil); err != nil {
		w.log.Printf("Listener %d: Error removing wakeUpRfd %d from epoll: %v\n", w.id, w.wakeUpRfd, err)
	}

	if err := w.closeWakeUpPipeFds(); err != nil {
		w.log.Println(err)
	}
}

func (w *Worker) closeWakeUpPipeFds() (err error) {
	w.log.Printf("Closing wake up pipe fds %d (read) and %d (write)\n", w.wakeUpRfd, w.wakeUpWfd)

	var readErr error
	if e := syscall.Close(w.wakeUpRfd); e != nil {
		readErr = fmt.Errorf("error closing wake up pipe read fd %d: %w", w.wakeUpRfd, e)
		w.log.Printf("%v", readErr)
	}

	var writeErr error
	if e := syscall.Close(w.wakeUpWfd); e != nil {
		writeErr = fmt.Errorf("error closing wake up pipe write fd %d: %w", w.wakeUpWfd, e)
		w.log.Printf("%v", writeErr)
	}

	return errors.Join(readErr, writeErr)
}
