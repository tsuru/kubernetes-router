FROM golang:1.9.0 as builder
COPY . /go/src/github.com/tsuru/ingress-router/
WORKDIR /go/src/github.com/tsuru/ingress-router/
RUN CGO_ENABLED=0 GOOS=linux go build

FROM alpine:latest  
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/src/github.com/tsuru/ingress-router/ingress-router .
CMD ["./ingress-router"]  