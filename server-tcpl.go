package main

import (
	"log"
	"net"
	"sync"
)

type serverTcpListener struct {
	p       *program
	netl    *net.TCPListener
	mutex   sync.RWMutex
	clients map[*serverClient]struct{}
}

func newServerTcpListener(p *program) (*serverTcpListener, error) {
	netl, err := net.ListenTCP("tcp", &net.TCPAddr{
		Port: p.conf.Server.RtspPort,
	})
	if err != nil {
		return nil, err
	}

	s := &serverTcpListener{
		p:       p,
		netl:    netl,
		clients: make(map[*serverClient]struct{}),
	}

	s.log("opened on :%d", p.conf.Server.RtspPort)
	return s, nil
}

func (l *serverTcpListener) log(format string, args ...interface{}) {
	log.Printf("[TCP listener] "+format, args...)
}

func (l *serverTcpListener) run() {
	for {
		nconn, err := l.netl.AcceptTCP()
		if err != nil {
			break
		}

		rsc := newServerClient(l.p, nconn)
		go rsc.run()
	}
}
