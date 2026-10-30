include ../mk/common.mk

# Where the plakman checkout is. The rig builds and runs plakman from source,
# and drives plakman's own test suite, so it needs the tree, not just a binary.
PLAKMAN_SRC ?= $(SRC_ROOT)/plakman

# The parent plakman enrolls against. The rig runs core-api from its own compose.
CORE_API_SRC ?= $(SRC_ROOT)/core-api

# The CLI the v1 API scenarios drive the instance through.
PCPCTL_SRC ?= $(SRC_ROOT)/pcpctl
export PLAKMAN_SRC CORE_API_SRC PCPCTL_SRC

.PHONY: all build qa qa-keep instance scenarios api api-v1 api-v2 bench clean

all: build

build:
	$(GO) build -o bin/ ./cmd/...

# One QA run: stand an instance up, drive plakman's suite against it, report.
# KEEP=1 leaves it running to poke at.
qa:
	@rig/qa.sh

qa-keep:
	@KEEP=1 rig/qa.sh

# An enrolled, seeded instance, left standing and running no tests. What you
# want while writing scenarios against it.
instance:
	@KEEP=1 SUITE='^$$' rig/qa.sh

# Our own scenarios, against a standing instance (see `make instance`).
scenarios:
	$(GO) test ./scenarios/... -count=1 -v

# The API contract scenarios, against a standing instance: v1 through pcpctl,
# then v2 mirroring every v1 scenario. See api/README.md.
api: api-v1 api-v2

api-v1: bin/pcpctl
	PCPCTL_BIN=$(CURDIR)/bin/pcpctl $(GO) test ./api/v1/... -count=1 -v

api-v2:
	$(GO) test ./api/v2/... -count=1 -v

bin/pcpctl:
	cd $(PCPCTL_SRC) && $(GO) build -o $(CURDIR)/bin/pcpctl ./cmd/pcpctl

bench:
	@echo 'plakman: no benchmarks yet'

clean:
	rm -rf bin/
