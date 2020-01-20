
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
  rtpPort: 8050
  rtcpPort: 8051

streams:
  test1:
    url: rtsp://localhost:8554/mystream

```
