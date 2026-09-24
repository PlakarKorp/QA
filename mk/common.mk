# Included by every project Makefile. Each project defines `qa` and `bench`.

GO ?= go

# The project checkouts are expected beside QA, e.g. ~/src/QA and ~/src/plakman.
QA_ROOT  := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))..)
SRC_ROOT ?= $(abspath $(QA_ROOT)/..)

.PHONY: qa bench
