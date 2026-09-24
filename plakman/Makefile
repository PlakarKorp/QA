include ../mk/common.mk

# Where the plakman checkout is. The rig builds and runs plakman from source,
# and drives plakman's own test suite, so it needs the tree, not just a binary.
PLAKMAN_SRC ?= $(SRC_ROOT)/plakman

.PHONY: all build qa qa-keep instance scenarios clean

all: build

build:
	$(GO) build -o bin/ ./cmd/...

# One QA run: stand an instance up, drive plakman's suite against it, report.
# KEEP=1 leaves it running to poke at.
qa:
	@PLAKMAN_SRC=$(PLAKMAN_SRC) rig/qa.sh

qa-keep:
	@KEEP=1 PLAKMAN_SRC=$(PLAKMAN_SRC) rig/qa.sh

# An enrolled, seeded instance, left standing and running no tests. What you
# want while writing scenarios against it.
instance:
	@KEEP=1 SUITE='^$$' PLAKMAN_SRC=$(PLAKMAN_SRC) rig/qa.sh

# Our own scenarios, against a standing instance (see `make instance`).
scenarios:
	$(GO) test ./scenarios/... -count=1 -v

clean:
	rm -rf bin/
