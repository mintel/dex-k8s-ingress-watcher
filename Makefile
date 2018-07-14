

OUT_BIN := ./bin/app

.PHONY: install-deps
install-deps: check-deps
	dep ensure

build: install-deps
	go build -o $(OUT_BIN) main.go

clean:
	rm -rf $(OUT_BIN)

.PHONY: run
run: build
	$(OUT_BIN)

HAS_DEP          := $(shell command -v dep;)

.PHONY: check-deps
check-deps:
ifndef HAS_DEP
	$(error You must install dep, you can install with: go get -u github.com/golang/dep/cmd/dep)
endif
