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
	_KEEPALIVE_INTERVAL    = 20 * time.Second
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

func writeReqReadRes(conn *gortsplib.ConnClient, req *gortsplib.Request) (*gortsplib.Response, error) {
	conn.NetConn().SetWriteDeadline(time.Now().Add(_WRITE_TIMEOUT))
	err := conn.WriteRequest(req)
	if err != nil {
		return nil, err
	}

	conn.NetConn().SetReadDeadline(time.Now().Add(_READ_TIMEOUT))
	return conn.ReadResponse()
}

type streamUdpListenerPair struct {
	rtpl  *streamUdpListener
	rtcpl *streamUdpListener
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
}

func newStream(p *program, path string, conf streamConf) (*stream, error) {
	ur, err := url.Parse(conf.Url)
	if err != nil {
		return nil, err
	}

	if ur.Scheme != "rtsp" {
		return nil, fmt.Errorf("unsupported scheme: %s", ur.Scheme)
	}

	if ur.Port() == "" {
		ur.Host = ur.Hostname() + ":554"
	}

	proto := _STREAM_PROTOCOL_UDP
	if conf.UseTcp {
		proto = _STREAM_PROTOCOL_TCP
	}

	s := &stream{
		p:     p,
		state: _STREAM_STATE_STARTING,
		path:  path,
		conf:  conf,
		ur:    ur,
		proto: proto,
	}

	go s.run()

	return s, nil
}

func (s *stream) log(format string, args ...interface{}) {
	format = "[STREAM " + s.path + "] " + format
	log.Printf(format, args...)
}

func (s *stream) run() {
	firstTime := true

	for {
		if firstTime {
			firstTime = false
		} else {
			time.Sleep(_RETRY_INTERVAL)
		}

		s.log("initializing with protocol %s", s.proto)

		func() {
			nconn, err := net.DialTimeout("tcp", s.ur.Host, _DIAL_TIMEOUT)
			if err != nil {
				s.log("ERR: %s", err)
				return
			}
			defer nconn.Close()

			conn := gortsplib.NewConnClient(nconn)

			res, err := writeReqReadRes(conn, &gortsplib.Request{
				Method: "OPTIONS",
				Url:    "rtsp://" + s.ur.Host + "/",
			})
			if err != nil {
				s.log("ERR: %s", err)
				return
			}

			if res.StatusCode != 200 {
				s.log("ERR: OPTIONS returned code %d", res.StatusCode)
				return
			}

			if sxRaw, ok := res.Header["Session"]; ok && len(sxRaw) == 1 {
				sx, err := gortsplib.ReadHeaderSession(sxRaw[0])
				if err != nil {
					s.log("ERR: unable to parse session: %s", err)
					return
				}
				conn.SetSession(sx.Session)
			}

			res, err = writeReqReadRes(conn, &gortsplib.Request{
				Method: "DESCRIBE",
				Url: "rtsp://" + s.ur.Host + s.ur.Path + func() string {
					if s.ur.RawQuery != "" {
						return "?" + s.ur.RawQuery
					}
					return ""
				}(),
			})
			if err != nil {
				s.log("ERR: %s", err)
				return
			}

			if res.StatusCode == 401 {
				if s.ur.User == nil {
					s.log("ERR: 401 but user not provided")
					return
				}

				user := s.ur.User.Username()
				pass, _ := s.ur.User.Password()
				if pass == "" {
					s.log("ERR: 401 but password not provided")
					return
				}

				err = conn.SetCredentials(res.Header["WWW-Authenticate"], user, pass)
				if err != nil {
					s.log("ERR: unable to set credentials: %s", err)
					return
				}

				res, err = writeReqReadRes(conn, &gortsplib.Request{
					Method: "DESCRIBE",
					Url: "rtsp://" + s.ur.Host + s.ur.Path + func() string {
						if s.ur.RawQuery != "" {
							return "?" + s.ur.RawQuery
						}
						return ""
					}(),
				})
				if err != nil {
					s.log("ERR: %s", err)
					return
				}
			}

			if res.StatusCode != 200 {
				s.log("ERR: DESCRIBE returned code %d", res.StatusCode)
				return
			}

			contentType, ok := res.Header["Content-Type"]
			if !ok || len(contentType) != 1 {
				s.log("ERR: Content-Type not provided")
				return
			}

			if contentType[0] != "application/sdp" {
				s.log("ERR: wrong Content-Type, expected application/sdp")
				return
			}

			clientSdpParsed, err := sdpParse(res.Content)
			if err != nil {
				s.log("ERR: invalid SDP: %s", err)
				return
			}

			// create a filtered SDP that is used by the server (not by the client)
			serverSdpParsed, serverSdpText := sdpFilter(clientSdpParsed, res.Content)

			func() {
				s.p.mutex.Lock()
				defer s.p.mutex.Unlock()

				s.clientSdpParsed = clientSdpParsed
				s.serverSdpText = serverSdpText
				s.serverSdpParsed = serverSdpParsed
			}()

			if s.proto == _STREAM_PROTOCOL_UDP {
				s.runUdp(conn)
			} else {
				s.runTcp(conn)
			}
		}()
	}
}

func (s *stream) runUdp(conn *gortsplib.ConnClient) {
	publisherAddr, err := net.ResolveUDPAddr("udp", s.ur.Hostname()+":0")
	if err != nil {
		s.log("ERR: %s", err)
		return
	}

	var streamUdpListenerPairs []streamUdpListenerPair

	defer func() {
		for _, pair := range streamUdpListenerPairs {
			pair.rtpl.close()
			pair.rtcpl.close()
		}
	}()

	for i, media := range s.clientSdpParsed.Medias {
		var rtpPort int
		var rtcpPort int
		var rtpl *streamUdpListener
		var rtcpl *streamUdpListener
		err := func() error {
			for {
				// choose two consecutive ports in range 65536-10000
				// rtp must be pair and rtcp odd
				rtpPort = (rand.Intn((65535-10000)/2) * 2) + 10000
				rtcpPort = rtpPort + 1

				rtpl, err = newStreamUdpListener(s.p, rtpPort)
				if err != nil {
					continue
				}

				rtcpl, err = newStreamUdpListener(s.p, rtcpPort)
				if err != nil {
					rtpl.close()
					continue
				}

				return nil
			}
		}()
		if err != nil {
			s.log("ERR: %s", err)
			return
		}

		res, err := writeReqReadRes(conn, &gortsplib.Request{
			Method: "SETUP",
			Url: "rtsp://" + s.ur.Host + func() string {
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
			}() + func() string {
				if s.ur.RawQuery != "" {
					return "?" + s.ur.RawQuery
				}
				return ""
			}(),
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
			rtpl.close()
			rtcpl.close()
			return
		}

		if res.StatusCode != 200 {
			s.log("ERR: SETUP returned code %d", res.StatusCode)
			rtpl.close()
			rtcpl.close()
			return
		}

		if sxRaw, ok := res.Header["Session"]; ok && len(sxRaw) == 1 {
			sx, err := gortsplib.ReadHeaderSession(sxRaw[0])
			if err != nil {
				s.log("ERR: unable to parse session: %s", err)
				return
			}
			conn.SetSession(sx.Session)
		}

		tsRaw, ok := res.Header["Transport"]
		if !ok || len(tsRaw) != 1 {
			s.log("ERR: transport header not provided")
			rtpl.close()
			rtcpl.close()
			return
		}

		th := gortsplib.ReadHeaderTransport(tsRaw[0])
		rtpServerPort, rtcpServerPort := th.GetPorts("server_port")
		if rtpServerPort == 0 {
			s.log("ERR: server ports not provided")
			rtpl.close()
			rtcpl.close()
			return
		}

		rtpl.publisherIp = publisherAddr.IP
		rtpl.publisherPort = rtpServerPort
		rtpl.trackId = i
		rtpl.flow = _TRACK_FLOW_RTP
		rtpl.path = s.path

		rtcpl.publisherIp = publisherAddr.IP
		rtcpl.publisherPort = rtcpServerPort
		rtcpl.trackId = i
		rtcpl.flow = _TRACK_FLOW_RTCP
		rtcpl.path = s.path

		streamUdpListenerPairs = append(streamUdpListenerPairs, streamUdpListenerPair{
			rtpl:  rtpl,
			rtcpl: rtcpl,
		})
	}

	res, err := writeReqReadRes(conn, &gortsplib.Request{
		Method: "PLAY",
		Url: "rtsp://" + s.ur.Host + s.ur.Path + func() string {
			if s.ur.RawQuery != "" {
				return "?" + s.ur.RawQuery
			}
			return ""
		}(),
	})
	if err != nil {
		s.log("ERR: %s", err)
		return
	}

	if res.StatusCode != 200 {
		s.log("ERR: PLAY returned code %d", res.StatusCode)
		return
	}

	for _, pair := range streamUdpListenerPairs {
		pair.rtpl.start()
		pair.rtcpl.start()
	}

	tickerSendKeepalive := time.NewTicker(_KEEPALIVE_INTERVAL)
	tickerCheckStream := time.NewTicker(_CHECK_STREAM_INTERVAL)

	func() {
		s.p.mutex.Lock()
		defer s.p.mutex.Unlock()
		s.state = _STREAM_STATE_READY
	}()

	defer func() {
		s.p.mutex.Lock()
		defer s.p.mutex.Unlock()
		s.state = _STREAM_STATE_STARTING

		// disconnect all clients
		for c := range s.p.clients {
			if c.path == s.path {
				c.close()
			}
		}
	}()

	s.log("ready")

	for {
		select {
		case <-tickerSendKeepalive.C:
			_, err = writeReqReadRes(conn, &gortsplib.Request{
				Method: "OPTIONS",
				Url:    "rtsp://" + s.ur.Host + "/",
			})
			if err != nil {
				s.log("ERR: %s", err)
				return
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
				getLastFrameTime(pair.rtpl)
				getLastFrameTime(pair.rtcpl)
			}

			if time.Since(lastFrameTime) >= _STREAM_DEAD_AFTER {
				s.log("ERR: stream is dead")
				return
			}
		}
	}
}

func (s *stream) runTcp(conn *gortsplib.ConnClient) {
	for i, media := range s.clientSdpParsed.Medias {
		interleaved := fmt.Sprintf("interleaved=%d-%d", (i * 2), (i*2)+1)

		res, err := writeReqReadRes(conn, &gortsplib.Request{
			Method: "SETUP",
			Url: "rtsp://" + s.ur.Host + func() string {
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
			}() + func() string {
				if s.ur.RawQuery != "" {
					return "?" + s.ur.RawQuery
				}
				return ""
			}(),
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
			return
		}

		if sxRaw, ok := res.Header["Session"]; ok && len(sxRaw) == 1 {
			sx, err := gortsplib.ReadHeaderSession(sxRaw[0])
			if err != nil {
				s.log("ERR: unable to parse session: %s", err)
				return
			}
			conn.SetSession(sx.Session)
		}

		if res.StatusCode != 200 {
			s.log("ERR: SETUP returned code %d", res.StatusCode)
			return
		}

		tsRaw, ok := res.Header["Transport"]
		if !ok || len(tsRaw) != 1 {
			s.log("ERR: transport header not provided")
			return
		}

		th := gortsplib.ReadHeaderTransport(tsRaw[0])

		_, ok = th[interleaved]
		if !ok {
			s.log("ERR: transport header does not have %s (%s)", interleaved, tsRaw[0])
			return
		}
	}

	res, err := writeReqReadRes(conn, &gortsplib.Request{
		Method: "PLAY",
		Url: "rtsp://" + s.ur.Host + s.ur.Path + func() string {
			if s.ur.RawQuery != "" {
				return "?" + s.ur.RawQuery
			}
			return ""
		}(),
	})
	if err != nil {
		s.log("ERR: %s", err)
		return
	}

	if res.StatusCode != 200 {
		s.log("ERR: PLAY returned code %d", res.StatusCode)
		return
	}

	func() {
		s.p.mutex.Lock()
		defer s.p.mutex.Unlock()
		s.state = _STREAM_STATE_READY
	}()

	defer func() {
		s.p.mutex.Lock()
		defer s.p.mutex.Unlock()
		s.state = _STREAM_STATE_STARTING

		// disconnect all clients
		for c := range s.p.clients {
			if c.path == s.path {
				c.close()
			}
		}
	}()

	s.log("ready")

	for {
		conn.NetConn().SetReadDeadline(time.Now().Add(_READ_TIMEOUT))
		frame, err := conn.ReadInterleavedFrame()
		if err != nil {
			s.log("ERR: %s", err)
			return
		}

		trackId, trackFlow := interleavedChannelToTrack(frame.Channel)

		func() {
			s.p.mutex.RLock()
			defer s.p.mutex.RUnlock()

			s.p.forwardTrack(s.path, trackId, trackFlow, frame.Content)
		}()
	}
}
