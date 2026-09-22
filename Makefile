GO ?= go

# Where the plakman checkout is. The rig builds and runs plakman from source,
# and drives plakman's own test suite, so it needs the tree, not just a binary.
PLAKMAN_SRC ?= ../plakman

.PHONY: all build qa qa-keep clean

all: build

build:
	$(GO) build -o bin/ ./cmd/...

# One QA run: stand an instance up, drive plakman's suite against it, report.
# KEEP=1 leaves it running to poke at.
qa:
	@PLAKMAN_SRC=$(PLAKMAN_SRC) rig/qa.sh

qa-keep:
	@KEEP=1 PLAKMAN_SRC=$(PLAKMAN_SRC) rig/qa.sh

clean:
	rm -rf bin/
