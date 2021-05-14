FROM --platform=$BUILDPLATFORM golang:latest as builder
ARG TARGETARCH
ENV GOARCH=$TARGETARCH

COPY . /go/src/github.com/tsuru/kubernetes-router/
WORKDIR /go/src/github.com/tsuru/kubernetes-router/
RUN CGO_ENABLED=0 make build

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/src/github.com/tsuru/kubernetes-router/kubernetes-router .

EXPOSE 8077

CMD ["./kubernetes-router"]