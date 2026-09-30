ARG alpine_version=3.23
ARG golang_version=1.26.2
FROM --platform=$BUILDPLATFORM golang:${golang_version}-alpine${alpine_version} as builder
ARG TARGETARCH
ENV GOARCH=$TARGETARCH
RUN apk update && apk add make

COPY . /go/src/github.com/tsuru/kubernetes-router/
WORKDIR /go/src/github.com/tsuru/kubernetes-router/
RUN CGO_ENABLED=0 make build

FROM alpine:${alpine_version}
RUN apk --no-cache add ca-certificates

ARG gke_auth_plugin_version=0.6.3
ARG aws_iam_authenticator_version=0.7.20
ARG TARGETARCH
RUN set -x \
  && apk add --update --no-cache curl \
  && curl -fsSL "https://github.com/traviswt/gke-auth-plugin/releases/download/${gke_auth_plugin_version}/gke-auth-plugin_Linux_$( [[ ${TARGETARCH} == 'amd64' ]] && echo 'x86_64' || echo ${TARGETARCH} ).tar.gz" \
  |  tar -C /usr/local/bin -xzvf- gke-auth-plugin \
  && gke-auth-plugin version \
  && curl -fsSL -o /usr/local/bin/aws-iam-authenticator "https://github.com/kubernetes-sigs/aws-iam-authenticator/releases/download/v${aws_iam_authenticator_version}/aws-iam-authenticator_${aws_iam_authenticator_version}_linux_${TARGETARCH}" \
  && chmod +x /usr/local/bin/aws-iam-authenticator \
  && aws-iam-authenticator version

WORKDIR /root/
COPY --from=builder /go/src/github.com/tsuru/kubernetes-router/kubernetes-router .

EXPOSE 8077

CMD ["./kubernetes-router"]
