package main

import (
	"log"
	"net"
	"time"
)

type udpWrite struct {
	addr *net.UDPAddr
	buf  []byte
}

type serverUdpListener struct {
	p     *program
	nconn *net.UDPConn
	flow  trackFlow
	write chan *udpWrite
	done  chan struct{}
}

func newServerUdpListener(p *program, port int, flow trackFlow) (*serverUdpListener, error) {
	nconn, err := net.ListenUDP("udp", &net.UDPAddr{
		Port: port,
	})
	if err != nil {
		return nil, err
	}

	l := &serverUdpListener{
		p:     p,
		nconn: nconn,
		flow:  flow,
		write: make(chan *udpWrite),
		done:  make(chan struct{}),
	}

	l.log("opened on :%d", port)
	return l, nil
}

func (l *serverUdpListener) log(format string, args ...interface{}) {
	var label string
	if l.flow == _TRACK_FLOW_RTP {
		label = "RTP"
	} else {
		label = "RTCP"
	}
	log.Printf("[UDP/"+label+" listener] "+format, args...)
}

func (l *serverUdpListener) run() {
	go func() {
		for w := range l.write {
			l.nconn.SetWriteDeadline(time.Now().Add(l.p.writeTimeout))
			l.nconn.WriteTo(w.buf, w.addr)
		}
	}()

	buf := make([]byte, 2048) // UDP MTU is 1400
	for {
		_, _, err := l.nconn.ReadFromUDP(buf)
		if err != nil {
			break
		}
	}

	close(l.write)

	close(l.done)
}

func (l *serverUdpListener) close() {
	l.nconn.Close()
	<-l.done
}
