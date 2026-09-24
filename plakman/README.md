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

    make qa            # one run, tears down after
    make qa-keep       # same, leaves the instance up to poke at

`PLAKMAN_SRC` points at the plakman tree (default `../plakman`); `S3_VERSION`,
`MIN_VERSIONS` and `RUN_DIR` override the rest. Logs land in `$RUN_DIR/logs`.

### The two phases

The run resets the instance in the middle, which is not tidiness:

1. **bootstrap**, against a plakman that has never enrolled *and* a parent that
   has never issued anything. These are the tests *of* enrollment -- one of them
   asserts the deployment has no organizations at all.
2. **everything else**, against an enrolled and seeded instance.

Wiping only one half leaves a split brain: the parent still holding
organizations plakman has forgotten, which fails a bootstrap assertion for
reasons that have nothing to do with plakman. `reset` does both.

### Pieces, if you want them by hand

    ./bin/mock-parent -listen :8081
    ./bin/enroll -parent http://localhost:8081 -plakman http://localhost:8080
    ./bin/seed   -parent http://localhost:8081 -plakman http://localhost:8080 \
                 -s3-version v1.1.6 -retention-min-versions 1
    ./bin/catalog -name s3

`enroll` reimplements `cmd/bootstrap`'s Enroll rather than calling it, because
that command hardcodes localhost:8080 and localhost:8081 and a rig has to point
at whatever ports it actually bound.

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
returns 200 where the test wants 404. Chased down and filed as
PlakarKorp/plakman#1544. The snapshot is genuinely delisted -- the count comes
from `repo.ListSnapshots()` against the kloset repository, and it drops from 4
to 2 -- but the VFS still resolves it and returns a populated tree, while an id
that never existed correctly 404s on the same route. So it reads as a lookup
that does not consult whatever prune updated, rather than a hole in the route's
error handling.

An earlier note here said an unknown snapshot answers 500. That was a bogus
*store* id, which is a different path; with a valid store it is 404.

## Ordering, and why it matters

Our scenarios run **before** plakman's suite, on purpose. Two of its tests --
`organization_settings_test` and `settings_rehome_test` -- point the
deployment-scoped packages repository at `mirror.example` and never put it back.
In process that is invisible: every run gets a fresh database. On an instance
anything else shares, it poisons the package feed, and the first thing we saw
was a 502 from an endpoint that was working fine.

Worth knowing beyond this rig: a test that writes deployment-scoped state and
does not restore it is fine in process and wrong everywhere else.

## The ledger

`bin/ledger` reads the daemon's own log -- the routes it mounts at startup
against the requests it answered -- so coverage needs no instrumentation and
counts any client, not just the Go suite. Every run writes `logs/ledger.txt`.

The untouched list is the point, and it is the work queue. It started at 38 and
is down to 6: `OPTIONS /`, `GET /{$}`, `GET /agent/token`, the resource update
and endpoint reads, and `GET /mnt/{mount_id}/download/{path...}`. The rest fell
to `scenarios/`.

One caution from building it: a concrete path can match several templates, and
the ledger has to pick the one the server would. Crediting
`/inventories/coverage` to `/inventories/{inventory_id}` left the literal route
looking untouched and undercounted the whole report -- it read 86% when the
truth was 98%. It now prefers the most specific match.

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
