package main

import (
	"log"
	"net"
)

type serverUdpListener struct {
	p     *program
	nconn *net.UDPConn
	flow  trackFlow
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
	buf := make([]byte, 2048) // UDP MTU is 1400

	for {
		l.nconn.ReadFromUDP(buf)
	}
}
