FROM amd64/alpine:3.11

ENV VERSION 0.6.0
RUN wget -O- https://github.com/aler9/rtsp-simple-server/releases/download/v${VERSION}/rtsp-simple-server_v${VERSION}_linux_amd64.tar.gz | tar xvzf - \
    && mv rtsp-simple-server /usr/bin

COPY start.sh /
RUN chmod +x /start.sh

ENTRYPOINT [ "/start.sh" ]
