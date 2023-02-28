FROM golang:alpine3.17 AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN apk add git
RUN go mod tidy
RUN go build -o /snipcart-webhook-server

FROM alpine:3.17.2
LABEL org.opencontainers.image.authors="bastian@debyltech.com"
COPY --from=builder /snipcart-webhook-server /bin/snipcart-webhook-server
VOLUME ["/conf"]
ENTRYPOINT ["/bin/snipcart-webhook-server"]