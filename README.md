# plakman-qa

Black-box QA for plakman: stand a real instance up, drive it the way a customer
would, and say what works before we promote an rc to stable.

`guidelines/release.md` already says to QA-test the RCs. This is that, and it
**informs** the release decision rather than blocking the tag — a gate nobody
trusts yet gets routed around, and we have no track record yet.

The plan this follows is `engineering/plans/2026-09-22-plakman-qa-release-gate.md`
in source-of-truth.

## What is here

    cmd/mock-parent   stands in for core-api during a QA run
    cmd/enroll        drives a fresh instance through enrollment
    cmd/seed          brings an enrolled instance to what the scenarios assume
    cmd/catalog       prints the catalog as the instance sees it
    rig/compose.qa.yaml  overrides plakman's compose for a QA stack

No scenarios of our own yet. plakman's own `tests/` already has the data-path
e2e layer (backup/prune/restore against MinIO over a real daemon) and
`PLAKMAN_DAEMON` already points it at a deployment it did not start, so the
first thing worth doing is running *those* rather than writing new ones.

## The mock parent

plakman asks its parent for four things: who is logging in, which organizations
they have, a badge naming one, and a license. Seven endpoints, all JWT, and
every one of them is verified through `client.NewPki`, which fetches the key
**from the parent itself**. Nothing is pinned. So a mock that serves the key it
signs with is accepted by an ordinary production build — no `-tags devel`, no
patched binary.

That last part is why we do not use `POST /bootstrap/enroll/offline` instead.
Offline licenses verify against hardcoded keys that only `SwapOfflineLicenseKeys`
can replace, and that seam does not exist outside `-tags devel`. Taking it would
mean QA could never test the binary we ship.

## Running it

Needs the plakman checkout beside this one, and `GOPRIVATE=github.com/PlakarKorp/*`.

    go build -o bin/ ./cmd/...

    ./bin/mock-parent -listen :8081
    ./bin/enroll -parent http://localhost:8081 -plakman http://localhost:8080
    ./bin/seed   -parent http://localhost:8081 -plakman http://localhost:8080 \
                 -s3-version v1.1.6 -retention-min-versions 1

`enroll` reimplements `cmd/bootstrap`'s Enroll rather than calling it, because
that command hardcodes localhost:8080 and localhost:8081 and a rig has to point
at whatever ports it actually bound.

Then plakman's own suite, against that instance:

    PLAKMAN_DAEMON=http://localhost:8080 \
    PLAKMAN_PARENT=http://localhost:8081 \
    PLAKMAN_TEST_MINIO_HOST=localhost \
    PLAKMAN_REQUIRE_INFRA=1 \
      go test -tags devel ./tests/ -run TestE2E

## What seeding has to state

A scenario inherits deployment policy, and two pieces of it decide whether the
scenario can pass at all. Neither is stated by the tests, so the seed states
them:

- **The retention floor.** `DefaultMinVersions` is 3 restore points per source,
  so a prune cannot take a source down to one and a scenario that expects it to
  fails with more snapshots than it wanted. `-retention-min-versions` sets it.
  It is organization-scoped, not deployment-scoped — only `packages` is the
  latter.
- **The integration version.** The catalog offers the newest version for the
  instance's edition, so "install s3" means something different from one week to
  the next. `-s3-version` pins it, which is what makes a run reproducible.

## Where the data path stands

It works. Against a real instance the e2e prune scenario installs s3 from the
**public feed** (no mirror needed — packages are published and signed), runs four
backups from MinIO source buckets into a MinIO store, and prunes them back to
two.

It still fails at the last assertion: browsing a snapshot removed by prune
returns 200 where the test wants 404 (the #794 regression check). The route is
live — authenticated, an unknown snapshot id returns 500 — so this looks like a
real finding rather than an artifact of the rig, but nobody has confirmed what
the 200 carries. It wants its own look before it becomes an issue.

## Environment constraints, learned the hard way

- Building plakman's `local` image needs BuildKit *and* an SSH agent with access
  to the private repos (`RUN --mount=type=ssh,required`). Another reason to test
  the published image instead of building one.
- plakman's compose publishes fixed host ports and `plakario_network` is
  external. The rig should bind ephemeral ports and create the network itself.
- `cached` binds a unix socket under the state directory, so a long state path
  fails with `bind: invalid argument`. Keep it short.
- The executor wants `SSH_AUTH_SOCK` and refuses to initialise without one.
- MinIO no longer serves its Docker Hub image; `rig/compose.qa.yaml` points at
  quay.io.
- The credential-free seed is smaller than expected: `.seed-admin.yaml`'s minio
  connectors already hardcode `minioadmin`. Only SFTP and the AWS/Scaleway
  entries need gopass.
- The distribution tree is `<edition>/<plugin api>/<name>/<file>`, where the api
  segment is `pkg.PLUGIN_API_VERSION` (v1.1.0) — *not* the version in the index
  filename (`integrations-v1.0.0.json`). Signatures are `.ptar.sum.sig`. Both
  are easy to guess wrong, and guessing wrong looks exactly like a missing or
  unsigned package.

## On the plakman dependency

The plan says scenarios should reach plakman only through `client`. That holds
for scenarios. The mock parent is not a scenario — it has to mint the badges,
challenges and licenses plakman will verify, so it imports `credentials`, `pki`
and `api/v1/contract` as well. Worth keeping that split honest if a depguard
rule ever lands here.
