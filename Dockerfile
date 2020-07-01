FROM golang:1.12-alpine3.11 as build

RUN apk add --no-cache --update alpine-sdk=1.0-r0 \
                                bash=5.0.11-r1

ENV GO111MODULE=on

WORKDIR /app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN make build

FROM alpine:3.11

RUN apk add --no-cache --update ca-certificates=20191127-r2 \
                                openssl=1.1.1g-r0 \
                                curl=7.67.0-r0 && \
    addgroup -g 1000 -S mintel && \
    adduser -u 1000 -S mintel -G mintel

RUN mkdir -p /app/bin
COPY --from=build /app/bin/dex-k8s-ingress-watcher /app/bin/

# Add any required certs/key by mounting a volume on /certs
# The entrypoint will copy them and run update-ca-certificates at startup
RUN mkdir -p /certs

WORKDIR /app

COPY entrypoint.sh /
RUN chmod a+x /entrypoint.sh

USER 1000:1000

ENTRYPOINT ["/entrypoint.sh"]

CMD ["--help"]

