OUT_BIN := ./bin/dex-k8s-ingress-watcher

OS = $(shell uname)

# Project variables
BINARY_NAME ?= dex-k8s-ingress-watcher
DOCKER_REGISTRY ?= mintel
DOCKER_IMAGE = ${DOCKER_REGISTRY}/dex-k8s-ingress-watcher

VERSION ?= $(shell echo `git symbolic-ref -q --short HEAD || git describe --tags --exact-match` | tr '[/]' '-')
COMMIT_HASH ?= $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date +%FT%T%z)
LDFLAGS += -X main.version=${VERSION} -X main.commitHash=${COMMIT_HASH} -X main.buildDate=${BUILD_DATE}

# Docker variables
DOCKER_TAG ?= ${VERSION}
GOPROXY ?= ""

build:
	GO111MODULE=on go mod download
	GO111MODULE=on go build -o $(OUT_BIN) main.go

clean:
	rm -rf $(OUT_BIN)

.PHONY: docker
docker: ## Build Docker image
	docker build -t ${DOCKER_IMAGE}:${DOCKER_TAG} --build-arg GOPROXY=${GOPROXY} -f Dockerfile .
ifeq (${DOCKER_LATEST}, 1)
	docker tag ${DOCKER_IMAGE}:${DOCKER_TAG} ${DOCKER_IMAGE}:latest
endif
ifeq (${DOCKER_LATEST_CI}, 1)
	docker tag ${DOCKER_IMAGE}:${DOCKER_TAG} ${DOCKER_IMAGE}:latest-ci
endif

.PHONY: run
run: build
	$(OUT_BIN)

