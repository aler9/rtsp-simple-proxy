package main

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aler9/gortsplib"
	"gortc.io/sdp"
)

const (
	_DIAL_TIMEOUT          = 10 * time.Second
	_RETRY_INTERVAL        = 5 * time.Second
	_CHECK_STREAM_INTERVAL = 6 * time.Second
	_STREAM_DEAD_AFTER     = 5 * time.Second
	_KEEPALIVE_INTERVAL    = 60 * time.Second
)

func sdpParse(in []byte) (*sdp.Message, error) {
	s, err := sdp.DecodeSession(in, nil)
	if err != nil {
		return nil, err
	}

	m := &sdp.Message{}
	d := sdp.NewDecoder(s)
	err = d.Decode(m)
	if err != nil {
		return nil, err
	}

	if len(m.Medias) == 0 {
		return nil, fmt.Errorf("no tracks defined in SDP")
	}

	return m, nil
}

// remove everything from SDP except the bare minimum
func sdpFilter(msgIn *sdp.Message, byteIn []byte) (*sdp.Message, []byte) {
	msgOut := &sdp.Message{}

	msgOut.Name = "Stream"
	msgOut.Origin = sdp.Origin{
		Username:    "-",
		NetworkType: "IN",
		AddressType: "IP4",
		Address:     "127.0.0.1",
	}

	for i, m := range msgIn.Medias {
		var attributes []sdp.Attribute
		for _, attr := range m.Attributes {
			if attr.Key == "rtpmap" || attr.Key == "fmtp" {
				attributes = append(attributes, attr)
			}
		}

		// control attribute is mandatory, and is the path that is appended
		// to the stream path in SETUP
		attributes = append(attributes, sdp.Attribute{
			Key:   "control",
			Value: "trackID=" + strconv.FormatInt(int64(i), 10),
		})

		msgOut.Medias = append(msgOut.Medias, sdp.Media{
			Bandwidths: m.Bandwidths,
			Description: sdp.MediaDescription{
				Type:     m.Description.Type,
				Protocol: m.Description.Protocol,
				Formats:  m.Description.Formats,
			},
			Attributes: attributes,
		})
	}

	sdps := sdp.Session{}
	sdps = msgOut.Append(sdps)
	byteOut := sdps.AppendTo(nil)

	return msgOut, byteOut
}

type streamUdpListenerPair struct {
	udplRtp  *streamUdpListener
	udplRtcp *streamUdpListener
}

type streamState int

const (
	_STREAM_STATE_STARTING streamState = iota
	_STREAM_STATE_READY
)

type stream struct {
	p               *program
	state           streamState
	path            string
	conf            streamConf
	ur              *url.URL
	proto           streamProtocol
	clientSdpParsed *sdp.Message
	serverSdpText   []byte
	serverSdpParsed *sdp.Message
	firstTime       bool
	terminate       chan struct{}
	done            chan struct{}
}

func newStream(p *program, path string, conf streamConf) (*stream, error) {
	ur, err := url.Parse(conf.Url)
	if err != nil {
		return nil, err
	}

	if ur.Port() == "" {
		ur.Host = ur.Hostname() + ":554"
	}
	if conf.Protocol == "" {
		conf.Protocol = "udp"
	}

	if ur.Scheme != "rtsp" {
		return nil, fmt.Errorf("unsupported scheme: %s", ur.Scheme)
	}
	if ur.User != nil {
		pass, _ := ur.User.Password()
		user := ur.User.Username()
		if user != "" && pass == "" ||
			user == "" && pass != "" {
			fmt.Errorf("username and password must be both provided")
		}
	}

	proto, err := func() (streamProtocol, error) {
		switch conf.Protocol {
		case "udp":
			return _STREAM_PROTOCOL_UDP, nil

		case "tcp":
			return _STREAM_PROTOCOL_TCP, nil
		}
		return streamProtocol(0), fmt.Errorf("unsupported protocol: '%v'", conf.Protocol)
	}()
	if err != nil {
		return nil, err
	}

	s := &stream{
		p:         p,
		state:     _STREAM_STATE_STARTING,
		path:      path,
		conf:      conf,
		ur:        ur,
		proto:     proto,
		firstTime: true,
		terminate: make(chan struct{}),
		done:      make(chan struct{}),
	}

	return s, nil
}

func (s *stream) log(format string, args ...interface{}) {
	format = "[STREAM " + s.path + "] " + format
	log.Printf(format, args...)
}

func (s *stream) run() {
	for {
		ok := s.do()
		if !ok {
			break
		}
	}

	close(s.done)
}

func (s *stream) do() bool {
	if s.firstTime {
		s.firstTime = false
	} else {
		t := time.NewTimer(_RETRY_INTERVAL)
		select {
		case <-s.terminate:
			return false
		case <-t.C:
		}
	}

	s.log("initializing with protocol %s", s.proto)

	var nconn net.Conn
	var err error
	dialDone := make(chan struct{})
	go func() {
		nconn, err = net.DialTimeout("tcp", s.ur.Host, _DIAL_TIMEOUT)
		close(dialDone)
	}()

	select {
	case <-s.terminate:
		return false
	case <-dialDone:
	}

	if err != nil {
		s.log("ERR: %s", err)
		return true
	}
	defer nconn.Close()

	conn, err := gortsplib.NewConnClient(gortsplib.ConnClientConf{
		NConn: nconn,
		Username: func() string {
			if s.ur.User != nil {
				return s.ur.User.Username()
			}
			return ""
		}(),
		Password: func() string {
			if s.ur.User != nil {
				pass, _ := s.ur.User.Password()
				return pass
			}
			return ""
		}(),
		ReadTimeout:  s.p.readTimeout,
		WriteTimeout: s.p.writeTimeout,
	})
	if err != nil {
		s.log("ERR: %s", err)
		return true
	}

	res, err := conn.WriteRequest(&gortsplib.Request{
		Method: gortsplib.OPTIONS,
		Url: &url.URL{
			Scheme: "rtsp",
			Host:   s.ur.Host,
			Path:   "/",
		},
	})
	if err != nil {
		s.log("ERR: %s", err)
		return true
	}

	if res.StatusCode != 200 {
		s.log("ERR: OPTIONS returned code %d", res.StatusCode)
		return true
	}

	res, err = conn.WriteRequest(&gortsplib.Request{
		Method: gortsplib.DESCRIBE,
		Url: &url.URL{
			Scheme:   "rtsp",
			Host:     s.ur.Host,
			Path:     s.ur.Path,
			RawQuery: s.ur.RawQuery,
		},
	})
	if err != nil {
		s.log("ERR: %s", err)
		return true
	}

	if res.StatusCode != 200 {
		s.log("ERR: DESCRIBE returned code %d", res.StatusCode)
		return true
	}

	contentType, ok := res.Header["Content-Type"]
	if !ok || len(contentType) != 1 {
		s.log("ERR: Content-Type not provided")
		return true
	}

	if contentType[0] != "application/sdp" {
		s.log("ERR: wrong Content-Type, expected application/sdp")
		return true
	}

	clientSdpParsed, err := sdpParse(res.Content)
	if err != nil {
		s.log("ERR: invalid SDP: %s", err)
		return true
	}

	// create a filtered SDP that is used by the server (not by the client)
	serverSdpParsed, serverSdpText := sdpFilter(clientSdpParsed, res.Content)

	func() {
		s.p.tcpl.mutex.Lock()
		defer s.p.tcpl.mutex.Unlock()

		s.clientSdpParsed = clientSdpParsed
		s.serverSdpText = serverSdpText
		s.serverSdpParsed = serverSdpParsed
	}()

	if s.proto == _STREAM_PROTOCOL_UDP {
		return s.runUdp(conn)
	} else {
		return s.runTcp(conn)
	}
}

func (s *stream) runUdp(conn *gortsplib.ConnClient) bool {
	publisherIp := conn.NetConn().RemoteAddr().(*net.TCPAddr).IP

	var streamUdpListenerPairs []streamUdpListenerPair

	defer func() {
		for _, pair := range streamUdpListenerPairs {
			pair.udplRtp.close()
			pair.udplRtcp.close()
		}
	}()

	for i, media := range s.clientSdpParsed.Medias {
		var rtpPort int
		var rtcpPort int
		var udplRtp *streamUdpListener
		var udplRtcp *streamUdpListener
		func() {
			for {
				// choose two consecutive ports in range 65536-10000
				// rtp must be pair and rtcp odd
				rtpPort = (rand.Intn((65535-10000)/2) * 2) + 10000
				rtcpPort = rtpPort + 1

				var err error
				udplRtp, err = newStreamUdpListener(s.p, rtpPort)
				if err != nil {
					continue
				}

				udplRtcp, err = newStreamUdpListener(s.p, rtcpPort)
				if err != nil {
					udplRtp.close()
					continue
				}

				return
			}
		}()

		res, err := conn.WriteRequest(&gortsplib.Request{
			Method: gortsplib.SETUP,
			Url: &url.URL{
				Scheme: "rtsp",
				Host:   s.ur.Host,
				Path: func() string {
					ret := s.ur.Path

					if len(ret) == 0 || ret[len(ret)-1] != '/' {
						ret += "/"
					}

					control := media.Attributes.Value("control")
					if control != "" {
						ret += control
					} else {
						ret += "trackID=" + strconv.FormatInt(int64(i+1), 10)
					}

					return ret
				}(),
				RawQuery: s.ur.RawQuery,
			},
			Header: gortsplib.Header{
				"Transport": []string{strings.Join([]string{
					"RTP/AVP/UDP",
					"unicast",
					fmt.Sprintf("client_port=%d-%d", rtpPort, rtcpPort),
				}, ";")},
			},
		})
		if err != nil {
			s.log("ERR: %s", err)
			udplRtp.close()
			udplRtcp.close()
			return true
		}

		if res.StatusCode != 200 {
			s.log("ERR: SETUP returned code %d", res.StatusCode)
			udplRtp.close()
			udplRtcp.close()
			return true
		}

		tsRaw, ok := res.Header["Transport"]
		if !ok || len(tsRaw) != 1 {
			s.log("ERR: transport header not provided")
			udplRtp.close()
			udplRtcp.close()
			return true
		}

		th := gortsplib.ReadHeaderTransport(tsRaw[0])
		rtpServerPort, rtcpServerPort := th.GetPorts("server_port")
		if rtpServerPort == 0 {
			s.log("ERR: server ports not provided")
			udplRtp.close()
			udplRtcp.close()
			return true
		}

		udplRtp.publisherIp = publisherIp
		udplRtp.publisherPort = rtpServerPort
		udplRtp.trackId = i
		udplRtp.flow = _TRACK_FLOW_RTP
		udplRtp.path = s.path

		udplRtcp.publisherIp = publisherIp
		udplRtcp.publisherPort = rtcpServerPort
		udplRtcp.trackId = i
		udplRtcp.flow = _TRACK_FLOW_RTCP
		udplRtcp.path = s.path

		streamUdpListenerPairs = append(streamUdpListenerPairs, streamUdpListenerPair{
			udplRtp:  udplRtp,
			udplRtcp: udplRtcp,
		})
	}

	res, err := conn.WriteRequest(&gortsplib.Request{
		Method: gortsplib.PLAY,
		Url: &url.URL{
			Scheme:   "rtsp",
			Host:     s.ur.Host,
			Path:     s.ur.Path,
			RawQuery: s.ur.RawQuery,
		},
	})
	if err != nil {
		s.log("ERR: %s", err)
		return true
	}

	if res.StatusCode != 200 {
		s.log("ERR: PLAY returned code %d", res.StatusCode)
		return true
	}

	for _, pair := range streamUdpListenerPairs {
		pair.udplRtp.start()
		pair.udplRtcp.start()
	}

	tickerSendKeepalive := time.NewTicker(_KEEPALIVE_INTERVAL)
	defer tickerSendKeepalive.Stop()

	tickerCheckStream := time.NewTicker(_CHECK_STREAM_INTERVAL)
	defer tickerSendKeepalive.Stop()

	func() {
		s.p.tcpl.mutex.Lock()
		defer s.p.tcpl.mutex.Unlock()
		s.state = _STREAM_STATE_READY
	}()

	defer func() {
		s.p.tcpl.mutex.Lock()
		defer s.p.tcpl.mutex.Unlock()
		s.state = _STREAM_STATE_STARTING

		// disconnect all clients
		for c := range s.p.tcpl.clients {
			if c.path == s.path {
				c.close()
			}
		}
	}()

	s.log("ready")

	for {
		select {
		case <-s.terminate:
			return false

		case <-tickerSendKeepalive.C:
			_, err = conn.WriteRequest(&gortsplib.Request{
				Method: gortsplib.OPTIONS,
				Url: &url.URL{
					Scheme: "rtsp",
					Host:   s.ur.Host,
					Path:   "/",
				},
			})
			if err != nil {
				s.log("ERR: %s", err)
				return true
			}

		case <-tickerCheckStream.C:
			lastFrameTime := time.Time{}

			getLastFrameTime := func(l *streamUdpListener) {
				l.mutex.Lock()
				defer l.mutex.Unlock()
				if l.lastFrameTime.After(lastFrameTime) {
					lastFrameTime = l.lastFrameTime
				}
			}

			for _, pair := range streamUdpListenerPairs {
				getLastFrameTime(pair.udplRtp)
				getLastFrameTime(pair.udplRtcp)
			}

			if time.Since(lastFrameTime) >= _STREAM_DEAD_AFTER {
				s.log("ERR: stream is dead")
				return true
			}
		}
	}
}

func (s *stream) runTcp(conn *gortsplib.ConnClient) bool {
	for i, media := range s.clientSdpParsed.Medias {
		interleaved := fmt.Sprintf("interleaved=%d-%d", (i * 2), (i*2)+1)

		res, err := conn.WriteRequest(&gortsplib.Request{
			Method: gortsplib.SETUP,
			Url: &url.URL{
				Scheme: "rtsp",
				Host:   s.ur.Host,
				Path: func() string {
					ret := s.ur.Path

					if len(ret) == 0 || ret[len(ret)-1] != '/' {
						ret += "/"
					}

					control := media.Attributes.Value("control")
					if control != "" {
						ret += control
					} else {
						ret += "trackID=" + strconv.FormatInt(int64(i+1), 10)
					}

					return ret
				}(),
				RawQuery: s.ur.RawQuery,
			},
			Header: gortsplib.Header{
				"Transport": []string{strings.Join([]string{
					"RTP/AVP/TCP",
					"unicast",
					interleaved,
				}, ";")},
			},
		})
		if err != nil {
			s.log("ERR: %s", err)
			return true
		}

		if res.StatusCode != 200 {
			s.log("ERR: SETUP returned code %d", res.StatusCode)
			return true
		}

		tsRaw, ok := res.Header["Transport"]
		if !ok || len(tsRaw) != 1 {
			s.log("ERR: transport header not provided")
			return true
		}

		th := gortsplib.ReadHeaderTransport(tsRaw[0])

		_, ok = th[interleaved]
		if !ok {
			s.log("ERR: transport header does not have %s (%s)", interleaved, tsRaw[0])
			return true
		}
	}

	res, err := conn.WriteRequest(&gortsplib.Request{
		Method: gortsplib.PLAY,
		Url: &url.URL{
			Scheme:   "rtsp",
			Host:     s.ur.Host,
			Path:     s.ur.Path,
			RawQuery: s.ur.RawQuery,
		},
	})
	if err != nil {
		s.log("ERR: %s", err)
		return true
	}

	if res.StatusCode != 200 {
		s.log("ERR: PLAY returned code %d", res.StatusCode)
		return true
	}

	func() {
		s.p.tcpl.mutex.Lock()
		defer s.p.tcpl.mutex.Unlock()
		s.state = _STREAM_STATE_READY
	}()

	defer func() {
		s.p.tcpl.mutex.Lock()
		defer s.p.tcpl.mutex.Unlock()
		s.state = _STREAM_STATE_STARTING

		// disconnect all clients
		for c := range s.p.tcpl.clients {
			if c.path == s.path {
				c.close()
			}
		}
	}()

	s.log("ready")

	chanConnError := make(chan struct{})
	go func() {
		for {
			frame, err := conn.ReadInterleavedFrame()
			if err != nil {
				s.log("ERR: %s", err)
				close(chanConnError)
				break
			}

			trackId, trackFlow := interleavedChannelToTrack(frame.Channel)

			func() {
				s.p.tcpl.mutex.RLock()
				defer s.p.tcpl.mutex.RUnlock()

				s.p.tcpl.forwardTrack(s.path, trackId, trackFlow, frame.Content)
			}()
		}
	}()

	select {
	case <-s.terminate:
		return false
	case <-chanConnError:
		return true
	}
}

func (s *stream) close() {
	close(s.terminate)
	<-s.done
}
