# QA

QA passes and benchmarks for PlakarKorp projects, one directory per project.

    make -C plakman qa     # QA pass on one project
    make qa                # QA pass on every project
    make bench             # benchmarks on every project
    make list              # the projects

Every project is expected to be checked out beside this repository, and
`SRC_ROOT` says where if it is not. Each project documents its own knobs.

## Adding a project

Create `<project>/Makefile`, include `../mk/common.mk`, and define `qa` and
`bench`. Anything shared by more than one project goes in `mk/common.mk`.

## Coverage

```yaml
permissions:
  contents: read
  id-token: write

steps:
  # ... checkout, setup-go
  - run: go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
  - uses: PlakarKorp/QA/.github/actions/coverage@<sha>
    if: ${{ !cancelled() }}
    with:
      min-coverage: 80
```
