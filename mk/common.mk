# Included by every project Makefile. Each project defines `qa` and `bench`.

GO ?= go

# The project checkouts are expected beside QA, e.g. ~/src/QA and ~/src/plakman.
QA_TOP   := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))..)
SRC_ROOT ?= $(abspath $(QA_TOP)/..)

.PHONY: qa bench
