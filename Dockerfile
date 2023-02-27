FROM alpine:3.17.2

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
COPY *.go ./

RUN apk add --no-cache go git && \
    go mod tidy && \
    go build -o /bin/snipcart-webhook-server && \
    apk del go git

RUN rm go.mod go.sum *.go

VOLUME ['/conf']
ENTRYPOINT '/bin/snipcart-webhook-server'
CMD ['-config', '/conf/config.json']