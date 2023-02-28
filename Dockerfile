FROM alpine:3.17.2

WORKDIR /app

COPY go.mod ./
COPY *.go ./

RUN apk add --no-cache go git && \
    go mod tidy && \
    go build -o /bin/snipcart-webhook-server && \
    apk del go git

RUN rm *

VOLUME ["/conf"]
ENTRYPOINT ["/bin/snipcart-webhook-server"]
