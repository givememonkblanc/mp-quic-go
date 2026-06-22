GO ?= go
BINARY := ./bin/server
QLOGVIEWER := ./bin/qlogviewer
JETSON := ./bin/jetson
CONFIG ?= ./config/config.yaml

.PHONY: build test run clean tidy qlogviewer jetson

build:
	mkdir -p ./bin
	$(GO) build -o $(BINARY) ./cmd/server

qlogviewer: 
	mkdir -p ./bin
	$(GO) build -o $(QLOGVIEWER) ./cmd/qlogviewer

jetson:
	mkdir -p ./bin
	$(GO) build -o $(JETSON) ./cmd/jetson

test:
	$(GO) test ./...

run: build
	$(BINARY) --config $(CONFIG)

clean:
	rm -rf ./bin

tidy:
	$(GO) mod tidy
