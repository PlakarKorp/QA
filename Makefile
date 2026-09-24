# Every directory with a Makefile is a project. `make qa` and `make bench` run
# that target in each of them; `make -C <project> qa` runs one.
PROJECTS := $(patsubst %/Makefile,%,$(wildcard */Makefile))
TARGETS  := qa bench

.PHONY: all list $(TARGETS)

all: qa

list:
	@printf '%s\n' $(PROJECTS)

# Sequential on purpose: the rigs stand up real instances on fixed ports.
$(TARGETS):
	@set -e; for p in $(PROJECTS); do $(MAKE) -C $$p $@; done
