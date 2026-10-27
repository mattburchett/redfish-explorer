BIN     := bin/rfx
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build install run mock test live lint vendor clean

all: build

build:
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/rfx

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/rfx

run: build
	./$(BIN) $(ARGS)

mock:
	go run ./cmd/mockbmc

test:
	go test ./...

live:
	go test ./internal/redfish -run TestLive -v -count=1

lint:
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && echo 'gofmt: files need formatting' && exit 1)

vendor:
	go mod tidy
	go mod vendor

clean:
	rm -rf bin
