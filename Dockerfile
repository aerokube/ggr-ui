FROM alpine:3.12

RUN apk add -U tzdata ca-certificates && rm -Rf /var/cache/apk/*
COPY ggr-ui /usr/bin

EXPOSE 8888
ENTRYPOINT ["/usr/bin/ggr-ui", "-quota-dir", "/etc/grid-router/quota"]
