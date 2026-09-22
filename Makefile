GO ?= go

.PHONY: all build clean

all: build

build:
	$(GO) build -o bin/ ./cmd/...

clean:
	rm -rf bin/
