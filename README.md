
# rtsp-simple-proxy

[![Go Report Card](https://goreportcard.com/badge/github.com/aler9/rtsp-simple-proxy)](https://goreportcard.com/report/github.com/aler9/rtsp-simple-proxy)
[![Build Status](https://travis-ci.org/aler9/rtsp-simple-proxy.svg?branch=master)](https://travis-ci.org/aler9/rtsp-simple-proxy)

todo

## Installation

Precompiled binaries are available in the [release](https://github.com/aler9/rtsp-simple-proxy/releases) page. Just download and extract the executable.

## Usage

todo

```
server:
  protocols: [ tcp, udp ]
  rtspPort: 8554
  rtpPort: 8000
  rtcpPort: 8001

streams:
  test1:
    url: rtsp://10.0.0.11:554/path

  test2:
    url: rtsp://10.0.0.12:554/path
    useTcp: yes

  test3:
    url: rtsp://10.0.0.13:554/path
```
