FROM golang:1.10.1-alpine3.7

RUN apk add --no-cache --update alpine-sdk bash && \
    curl -fsSL -o /usr/local/bin/dep https://github.com/golang/dep/releases/download/v0.4.1/dep-linux-amd64 && \
    chmod +x /usr/local/bin/dep

COPY . /go/src/gitlab.com/mintel/dex-k8s-ingress-watcher
WORKDIR /go/src/gitlab.com/mintel/dex-k8s-ingress-watcher
RUN make build

FROM alpine:3.7
RUN apk add --update ca-certificates openssl curl

RUN mkdir -p /app/bin
COPY --from=0 /go/src/gitlab.com/mintel/dex-k8s-ingress-watcher/bin/dex-k8s-ingress-watcher /app/bin/dex-k8s-ingress-watcher

# Add any required certs/key by mounting a volume on /certs - Entrypoint will copy them and run update-ca-certificates at startup
RUN mkdir -p /certs

WORKDIR /app

COPY entrypoint.sh /
RUN chmod a+x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]

CMD ["--help"]

