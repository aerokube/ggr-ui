FROM alpine:3.8

RUN apk add -U tzdata && rm -Rf /var/cache/apk/*
COPY ggr-ui /usr/bin

EXPOSE 8888
ENTRYPOINT ["/usr/bin/ggr-ui", "-quota-dir", "/etc/grid-router/quota"]
