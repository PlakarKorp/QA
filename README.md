# plakman-qa

Black-box QA for plakman: stand a real instance up, drive it the way a customer
would, and say what works before we promote an rc to stable.

`guidelines/release.md` already says to QA-test the RCs. This is that, and it
**informs** the release decision rather than blocking the tag — a gate nobody
trusts yet gets routed around, and we have no track record yet.

The plan this follows is `engineering/plans/2026-09-22-plakman-qa-release-gate.md`
in source-of-truth.

## What is here today

Step 1 of the plan: enrollment, which was the assumption everything else rested
on. It works.

    cmd/mock-parent   stands in for core-api during a QA run
    cmd/enroll        drives a fresh instance through enrollment
    cmd/seed          the credential-free half of `make seed`
    rig/compose.qa.yaml  overrides plakman's compose for a QA stack

Nothing here writes scenarios yet. That is step 3, and it is blocked — see
below.

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
    make -C ../plakman ...   # bring up db, minio, and the QA override

    ./bin/mock-parent -listen :8081
    ./bin/enroll  -parent http://localhost:8081 -plakman http://localhost:8080
    ./bin/seed    -parent http://localhost:8081 -plakman http://localhost:8080

`enroll` reimplements `cmd/bootstrap`'s Enroll rather than calling it, because
that command hardcodes localhost:8080 and localhost:8081 and a rig has to point
at whatever ports it actually bound.

## What blocks the data path

Backup/restore needs the s3 integration, and installing it fails:

    package signature verification failed: s3_v1.1.6_linux_amd64.ptar:
    package is not signed

Probing the feed directly, with the path layout confirmed by an older package
that does resolve:

    community/v1.0.0/s3/s3_v1.0.8_linux_amd64.ptar        206
    community/v1.0.0/s3/s3_v1.0.8_linux_amd64.ptar.sig    404
    enterprise/v1.0.0/s3/s3_v1.1.6_linux_amd64.ptar       404

The index advertises `s3 enterprise v1.1.6`; no package exists where the
executor looks for it. The alternate package repository is a database setting,
not something the parent or the license hands over, so this is not the mock's
doing — a real core-api enrollment lands on the same public feed.

Unresolved: whether enterprise deployments are *expected* to configure their own
repository, which would make this working as designed rather than broken. Either
way the diagnostic is wrong: a missing package should not report as unsigned.

**So the rig needs its own signed package mirror.** Until it has one, a QA run
depends on whatever is published on the day, which is not a property a release
gate can have.

## Environment constraints, learned the hard way

- Building plakman's `local` image needs BuildKit *and* an SSH agent with access
  to the private repos (`RUN --mount=type=ssh,required`). Another reason to test
  the published image instead of building one.
- plakman's compose publishes fixed host ports and `plakario_network` is
  external. The rig should bind ephemeral ports and create the network itself.
- `cached` binds a unix socket under the state directory, so a long state path
  fails with `bind: invalid argument`. Keep it short.
- The executor wants `SSH_AUTH_SOCK`; compose supplies one at
  `/var/lib/plakman/ssh-agent.sock`.
- MinIO no longer serves its Docker Hub image; `rig/compose.qa.yaml` points at
  quay.io.
- The credential-free seed is smaller than expected: `.seed-admin.yaml`'s minio
  connectors already hardcode `minioadmin`. Only SFTP and the AWS/Scaleway
  entries need gopass.

## On the plakman dependency

The plan says scenarios should reach plakman only through `client`. That holds
for scenarios. The mock parent is not a scenario — it has to mint the badges,
challenges and licenses plakman will verify, so it imports `credentials`, `pki`
and `api/v1/contract` as well. Worth keeping that split honest if a depguard
rule ever lands here.
