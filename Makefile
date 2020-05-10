
.PHONY: $(shell ls)

BASE_IMAGE = amd64/golang:1.13-alpine3.10

help:
	@echo "usage: make [action]"
	@echo ""
	@echo "available actions:"
	@echo ""
	@echo "  mod-tidy       run go mod tidy"
	@echo "  format         format source files"
	@echo "  run ARGS=args  run app"
	@echo "  release        build release assets"
	@echo "  travis-setup   setup travis CI"
	@echo ""

blank :=
define NL

$(blank)
endef

mod-tidy:
	docker run --rm -it -v $(PWD):/s $(BASE_IMAGE) \
	sh -c "apk add git && cd /s && GOPROXY=direct go get && GOPROXY=direct go mod tidy"

format:
	docker run --rm -it -v $(PWD):/s $(BASE_IMAGE) \
	sh -c "cd /s && find . -type f -name '*.go' | xargs gofmt -l -w -s"

define DOCKERFILE_RUN
FROM $(BASE_IMAGE)
RUN apk add --no-cache git
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN GOPROXY=direct go build -o /out .
endef
export DOCKERFILE_RUN

define CONFFILE
server:
  protocols: [ tcp, udp ]
  rtspPort: 8555
  rtpPort: 8050
  rtcpPort: 8051

streams:
  test1:
    url: rtsp://localhost:8554/mystream

endef
export CONFFILE

run:
	echo "$$DOCKERFILE_RUN" | docker build -q . -f - -t temp
	echo "$$CONFFILE" | docker run --rm -i \
	--network=host \
	temp \
	/out stdin

define DOCKERFILE_TEST
FROM $(BASE_IMAGE)
RUN apk add --no-cache make docker-cli git
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
endef
export DOCKERFILE_TEST

test:
	echo "$$DOCKERFILE_TEST" | docker build -q . -f - -t temp
	docker run --rm -it \
	--network=host \
	-v /var/run/docker.sock:/var/run/docker.sock:ro \
	temp \
	make test-nodocker

test-nodocker:
	$(foreach IMG,$(shell echo test-images/*/ | xargs -n1 basename), \
	docker build -q test-images/$(IMG) -t rtsp-simple-proxy-test-$(IMG)$(NL))
	$(eval export CGO_ENABLED = 0)
	go test -v .

define DOCKERFILE_RELEASE
FROM $(BASE_IMAGE)
RUN apk add --no-cache zip make git tar
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN make release-nodocker
endef
export DOCKERFILE_RELEASE

release:
	echo "$$DOCKERFILE_RELEASE" | docker build . -f - -t temp \
	&& docker run --rm -it -v $(PWD):/out \
	temp sh -c "rm -rf /out/release && cp -r /s/release /out/"

release-nodocker:
	$(eval VERSION := $(shell git describe --tags))
	$(eval GOBUILD := go build -ldflags '-X "main.Version=$(VERSION)"')
	rm -rf release && mkdir release

	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) -o /tmp/rtsp-simple-proxy.exe
	cd /tmp && zip -q $(PWD)/release/rtsp-simple-proxy_$(VERSION)_windows_amd64.zip rtsp-simple-proxy.exe

	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o /tmp/rtsp-simple-proxy
	tar -C /tmp -czf $(PWD)/release/rtsp-simple-proxy_$(VERSION)_linux_amd64.tar.gz --owner=0 --group=0 rtsp-simple-proxy

	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 $(GOBUILD) -o /tmp/rtsp-simple-proxy
	tar -C /tmp -czf $(PWD)/release/rtsp-simple-proxy_$(VERSION)_linux_arm6.tar.gz --owner=0 --group=0 rtsp-simple-proxy

	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o /tmp/rtsp-simple-proxy
	tar -C /tmp -czf $(PWD)/release/rtsp-simple-proxy_$(VERSION)_linux_arm7.tar.gz --owner=0 --group=0 rtsp-simple-proxy

	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -o /tmp/rtsp-simple-proxy
	tar -C /tmp -czf $(PWD)/release/rtsp-simple-proxy_$(VERSION)_linux_arm64.tar.gz --owner=0 --group=0 rtsp-simple-proxy

	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) -o /tmp/rtsp-simple-proxy
	tar -C /tmp -czf $(PWD)/release/rtsp-simple-proxy_$(VERSION)_darwin_amd64.tar.gz --owner=0 --group=0 rtsp-simple-proxy

define DOCKERFILE_TRAVIS
FROM ruby:alpine
RUN apk add --no-cache build-base git
RUN gem install travis
endef
export DOCKERFILE_TRAVIS

travis-setup:
	echo "$$DOCKERFILE_TRAVIS" | docker build - -t temp
	docker run --rm -it \
	-v $(PWD):/s \
	temp \
	sh -c "cd /s \
	&& travis setup releases --force"
