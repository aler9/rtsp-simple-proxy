
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
  # supported protocols
  protocols: [ tcp, udp ]
  # port of the RTSP TCP listener
  rtspPort: 8554
  # port of the RTP UDP listener
  rtpPort: 8050
  # port of the RTCP UDP listener
  rtcpPort: 8051

streams:
  test1:
    url: rtsp://localhost:8554/mystream

```
