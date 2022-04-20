FROM golang:1.17-alpine3.15 as build

WORKDIR /go/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /go/bin/dex-k8s-ingress-watcher

FROM alpine:3.15

# Add any required certs/key by mounting a volume on /certs
# The entrypoint will copy them and run update-ca-certificates at startup
VOLUME ["/certs"]

WORKDIR /app

COPY entrypoint.sh /
RUN chmod a+x /entrypoint.sh

COPY --from=build /go/bin/dex-k8s-ingress-watcher /app/bin/

USER 1000:1000

ENTRYPOINT ["/entrypoint.sh"]
CMD ["--help"]
