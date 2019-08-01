OUT_BIN := ./bin/dex-k8s-ingress-watcher

build:
	go build -o $(OUT_BIN) main.go

clean:
	rm -rf $(OUT_BIN)

.PHONY: run
run: build
	$(OUT_BIN)
