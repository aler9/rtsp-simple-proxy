package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"gopkg.in/alecthomas/kingpin.v2"
	"gopkg.in/yaml.v2"
)

var Version string = "v0.0.0"

const (
	_READ_TIMEOUT  = 5 * time.Second
	_WRITE_TIMEOUT = 5 * time.Second
)

type trackFlow int

const (
	_TRACK_FLOW_RTP trackFlow = iota
	_TRACK_FLOW_RTCP
)

type track struct {
	rtpPort  int
	rtcpPort int
}

type streamProtocol int

const (
	_STREAM_PROTOCOL_UDP streamProtocol = iota
	_STREAM_PROTOCOL_TCP
)

func (s streamProtocol) String() string {
	if s == _STREAM_PROTOCOL_UDP {
		return "udp"
	}
	return "tcp"
}

type streamConf struct {
	Url    string `yaml:"url"`
	UseTcp bool   `yaml:"useTcp"`
}

type conf struct {
	Server struct {
		Protocols []string `yaml:"protocols"`
		RtspPort  int      `yaml:"rtspPort"`
		RtpPort   int      `yaml:"rtpPort"`
		RtcpPort  int      `yaml:"rtcpPort"`
	} `yaml:"server"`
	Streams map[string]streamConf `yaml:"streams"`
}

func loadConf(confPath string) (*conf, error) {
	if confPath == "stdin" {
		var ret conf
		err := yaml.NewDecoder(os.Stdin).Decode(&ret)
		if err != nil {
			return nil, err
		}

		return &ret, nil

	} else {
		f, err := os.Open(confPath)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		var ret conf
		err = yaml.NewDecoder(f).Decode(&ret)
		if err != nil {
			return nil, err
		}

		return &ret, nil
	}
}

type args struct {
	version  bool
	confPath string
}

type program struct {
	conf      conf
	protocols map[streamProtocol]struct{}
	streams   map[string]*stream
	tcpl      *serverTcpListener
	udplRtp   *serverUdpListener
	udplRtcp  *serverUdpListener
}

func newProgram(args args) (*program, error) {
	if args.version == true {
		fmt.Println("rtsp-simple-proxy " + Version)
		os.Exit(0)
	}

	conf, err := loadConf(args.confPath)
	if err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	if conf.Server.RtspPort == 0 {
		return nil, fmt.Errorf("rtsp port not provided")
	}

	if conf.Server.RtpPort == 0 {
		return nil, fmt.Errorf("rtp port not provided")
	}

	if conf.Server.RtcpPort == 0 {
		return nil, fmt.Errorf("rtcp port not provided")
	}

	if (conf.Server.RtpPort % 2) != 0 {
		return nil, fmt.Errorf("rtp port must be even")
	}

	if conf.Server.RtcpPort != (conf.Server.RtpPort + 1) {
		return nil, fmt.Errorf("rtcp port must be rtp port plus 1")
	}

	protocols := make(map[streamProtocol]struct{})
	for _, proto := range conf.Server.Protocols {
		switch proto {
		case "udp":
			protocols[_STREAM_PROTOCOL_UDP] = struct{}{}

		case "tcp":
			protocols[_STREAM_PROTOCOL_TCP] = struct{}{}

		default:
			return nil, fmt.Errorf("unsupported protocol: %s", proto)
		}
	}
	if len(protocols) == 0 {
		return nil, fmt.Errorf("no protocols provided")
	}

	if len(conf.Streams) == 0 {
		return nil, fmt.Errorf("no streams provided")
	}

	log.Printf("rtsp-simple-proxy %s", Version)

	p := &program{
		conf:      *conf,
		protocols: protocols,
		streams:   make(map[string]*stream),
	}

	for path, val := range p.conf.Streams {
		var err error
		p.streams[path], err = newStream(p, path, val)
		if err != nil {
			return nil, err
		}
	}

	p.udplRtp, err = newServerUdpListener(p, p.conf.Server.RtpPort, _TRACK_FLOW_RTP)
	if err != nil {
		return nil, err
	}

	p.udplRtcp, err = newServerUdpListener(p, p.conf.Server.RtcpPort, _TRACK_FLOW_RTCP)
	if err != nil {
		return nil, err
	}

	p.tcpl, err = newServerTcpListener(p)
	if err != nil {
		return nil, err
	}

	for _, s := range p.streams {
		go s.run()
	}

	go p.udplRtp.run()
	go p.udplRtcp.run()
	go p.tcpl.run()

	return p, nil
}

func (p *program) close() {
	for _, s := range p.streams {
		s.close()
	}

	p.tcpl.close()
	p.udplRtcp.close()
	p.udplRtp.close()
}

func main() {
	kingpin.CommandLine.Help = "rtsp-simple-proxy " + Version + "\n\n" +
		"RTSP proxy."

	argVersion := kingpin.Flag("version", "print rtsp-simple-proxy version").Bool()
	argConfPath := kingpin.Arg("confpath", "path of the config file. Use 'stdin' to read config from stdin").Required().String()

	kingpin.Parse()

	_, err := newProgram(args{
		version:  *argVersion,
		confPath: *argConfPath,
	})
	if err != nil {
		log.Fatal("ERR: ", err)
	}

	infty := make(chan struct{})
	<-infty
}
