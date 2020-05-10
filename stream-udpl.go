package main

import (
	"net"
	"sync"
	"time"
)

type streamUdpListenerState int

const (
	_UDPL_STATE_STARTING streamUdpListenerState = iota
	_UDPL_STATE_RUNNING
)

type streamUdpListener struct {
	p             *program
	nconn         *net.UDPConn
	state         streamUdpListenerState
	done          chan struct{}
	publisherIp   net.IP
	publisherPort int
	trackId       int
	flow          trackFlow
	path          string
	mutex         sync.Mutex
	lastFrameTime time.Time
}

func newStreamUdpListener(p *program, port int) (*streamUdpListener, error) {
	nconn, err := net.ListenUDP("udp", &net.UDPAddr{
		Port: port,
	})
	if err != nil {
		return nil, err
	}

	l := &streamUdpListener{
		p:     p,
		nconn: nconn,
		state: _UDPL_STATE_STARTING,
		done:  make(chan struct{}),
	}

	return l, nil
}

func (l *streamUdpListener) close() {
	l.nconn.Close()

	if l.state == _UDPL_STATE_RUNNING {
		<-l.done
	}
}

func (l *streamUdpListener) start() {
	l.state = _UDPL_STATE_RUNNING
	go l.run()
}

func (l *streamUdpListener) run() {
	for {
		// create a buffer for each read.
		// this is necessary since the buffer is propagated with channels
		// so it must be unique.
		buf := make([]byte, 2048) // UDP MTU is 1400
		n, addr, err := l.nconn.ReadFromUDP(buf)
		if err != nil {
			break
		}

		if !l.publisherIp.Equal(addr.IP) || addr.Port != l.publisherPort {
			continue
		}

		func() {
			l.p.rtspl.mutex.RLock()
			defer l.p.rtspl.mutex.RUnlock()

			l.p.rtspl.forwardTrack(l.path, l.trackId, l.flow, buf[:n])
		}()

		func() {
			l.mutex.Lock()
			defer l.mutex.Unlock()
			l.lastFrameTime = time.Now()
		}()
	}

	l.done <- struct{}{}
}
