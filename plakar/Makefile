include ../mk/common.mk

# The fuzz targets build against the plakar checkout beside QA; go.mod says
# so with a replace directive, which is what the OSS-Fuzz builder's layout
# ($SRC/QA and $SRC/plakar) gives as well.

.PHONY: qa bench fuzz

# Every target's seeds as regression tests.
qa:
	$(GO) test ./...

# One target under the Go fuzzer: make fuzz TARGET=FuzzPackfileCodec
TARGET   ?= FuzzPackfileCodec
FUZZTIME ?= 60s
fuzz:
	$(GO) test -fuzz=$(TARGET) -fuzztime=$(FUZZTIME) ./fuzz

bench:
	@echo 'plakar: no benchmarks yet'
