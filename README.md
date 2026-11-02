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

## Integrations

`.github/workflows/integration.yml` validates, lints and tests a plakar
integration. The integration's repository calls it:

```yaml
jobs:
  qa:
    permissions:
      contents: read
      id-token: write
    uses: PlakarKorp/QA/.github/workflows/integration.yml@<sha>
    with: { dir: s3, qa-version-file: s3/.qa-version }
```

`id-token: write` lets the test job upload its coverage to kortex, flagged
with `dir`. Without it the upload is a warning, never a failed job.

Upload from the integration's own branch, `integration/<name>`. In a
repository holding several integrations behind `paths:` filters, a commit on
`main` only has the coverage of the integrations it touched.

kortex maps the profile to the files git tracks by path suffix, so the module
path must end with `dir`: `github.com/PlakarKorp/integrations/s3` in `s3/`.
A module that does not, such as `github.com/PlakarKorp/integration-docker` in
`docker/`, has its files skipped and uploads no coverage.
