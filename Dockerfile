FROM golang:1.12-alpine3.10


RUN apk add --no-cache --update alpine-sdk bash

ENV GO111MODULE=on

WORKDIR /app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN make build

FROM alpine:3.10.1

RUN apk add --update ca-certificates openssl curl && \
    addgroup -g 1000 -S mintel && \
    adduser -u 1000 -S mintel -G mintel

RUN mkdir -p /app/bin
COPY --from=0 /app/bin/dex-k8s-ingress-watcher /app/bin/

# Add any required certs/key by mounting a volume on /certs
# The entrypoint will copy them and run update-ca-certificates at startup
RUN mkdir -p /certs

WORKDIR /app

COPY entrypoint.sh /
RUN chmod a+x /entrypoint.sh

USER 1000:1000

ENTRYPOINT ["/entrypoint.sh"]

CMD ["--help"]

