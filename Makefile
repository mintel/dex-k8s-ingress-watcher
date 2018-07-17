

OUT_BIN := ./bin/dex-k8s-ingress-watcher

.PHONY: install-deps
install-deps: check-deps
	dep ensure

build: install-deps
	go build -o $(OUT_BIN) main.go

clean:
	rm -rf $(OUT_BIN)
	rm -rf vendor

.PHONY: run
run: build
	$(OUT_BIN)

HAS_DEP          := $(shell command -v dep;)

.PHONY: check-deps
check-deps:
ifndef HAS_DEP
	$(error You must install dep, you can install with: go get -u github.com/golang/dep/cmd/dep)
endif
