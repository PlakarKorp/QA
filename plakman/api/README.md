# The API contract scenarios

`make qa` runs these after the coverage scenarios and before plakman's own
suite. `make api` runs them against a standing instance (`make instance`),
`make api-v1`, `make api-v2` and `make api-pcpctl-v2` one phase at a time.

    api/harness   connects to the instance, runs pcpctl, speaks v2
    api/v1        the v1 contract, as pcpctl drives it
    api/v2        the same scenarios on v2, named one for one

The v1 scenarios run twice: once with pcpctl on its v1 wire, and once more
with `PCPCTL_API=v2`, pcpctl speaking v2 underneath the same commands. The
second run asserts the same exit codes and shapes; where v2 has no route for
what a command does, the scenario asserts pcpctl's own refusal instead --
exit 1, saying the feature is not available on API v2 -- through
`pcpctl.NotOnV2`. A red line in that phase is pcpctl's two wires disagreeing,
not a parity gap.

Both sides act as the seeded administrator through one API key the harness
mints at startup, which is how an automation reaches a control plane.

## v1, through pcpctl

The v1 scenarios never speak HTTP. Each runs the pcpctl binary the rig built
(`bin/pcpctl`, or `PCPCTL_BIN`) and asserts what a script would see: the exit
code pcpctl documents (0, 1 failure, 2 usage, 3 not found, 4 conflict, 5
unauthorized), the JSON `-o json` prints, and what a listing shows after a
create and after a delete. A green run therefore says the v1 contract *as
pcpctl presents it* has not moved -- which is the contract a customer's script
holds.

A scenario is a `Test` function named for the feature, with the misuse paths
as subtests: `TestOrganizationLifecycle`, `TestOrganizationDeleteMisuse`, and
so on, service by service. Every scenario creates what it needs under a unique
name and removes it in a `t.Cleanup`, so the suite shares one instance with
anything else running against it and still finds only its own rows.

### Adding a scenario

1. Write `TestSomething` in the `api/v1` file of its service, driving pcpctl
   through `pcpctl.OK`, `pcpctl.JSON` and `pcpctl.Fails(t, harness.ExitX, ...)`.
   Assert the exact outcome: the code, the fields, the listing.
2. Write `TestSomething` in `api/v2`, same name, reaching the same feature
   over v2 with `v2.OK`, `v2.Refused(t, ..., harness.CodeX)` and
   `harness.List`. One pcpctl command is often several v2 calls.
3. If v2 has no way to do it, the v2 scenario calls `gap(t, "...")`, naming
   the v1 feature. It fails, on purpose, until v2 can.

`TestParity` in `api/v2` reads both packages' sources and fails for any v1
scenario without a v2 function of the same name, so step 2 cannot be skipped
quietly.

### Findings

A behaviour that cannot be asserted as a contract -- the server falls over, or
pcpctl and the API disagree -- is recorded with `finding(t, "...")`: the
subtest is skipped, with the finding in its name, so the run stays green and
the summary still shows it. Fix the finding, drop the skip, and the assertion
underneath it becomes the contract. The findings as of this writing:

v1

- `GET /organizations/{id}/audit-logs` ignores the `event=` and `actor=`
  filters pcpctl sends.
- `GET /scheduling/jobs?status=bogus` answers 500.
- `POST /integrations/installed` with a name the catalogue does not offer
  answers 500.
- `PUT /scheduling/scheduler/roles` accepts a role name the catalogue does not
  have.
- `DELETE /packages/{name}/{version}` of a package never installed answers 200.
- The `organization.created` entry that opens a new organization's audit log
  names no actor (v2 shows the same).
- `POST /integrations/installed/current` with a name the catalogue does not
  offer answers 500, as `POST /integrations/installed` does.
- `POST /account/organizations/{id}/secret_providers` with an integration that
  is not a secret provider (s3) answers 500.
- `POST /update-to-version` with a version the feed does not offer answers
  500 through the mock orkestrator.
- `POST /inventories/{id}/resources/bulk` is documented as adding resources
  and replaces the inventory's resources instead; the scenario imports into
  an inventory of its own.
- `GET /inventories/{id}/resources/{urn_id}/endpoints` of a resource that does
  not exist answers 200 with no endpoints; every other resource route 404s.
- `POST /account/organizations/{id}/inventories`, the generic creation route,
  answers 500 for a provider whose integration is not installed, where the
  per-provider route answers 404.
- `POST /account/organizations/{id}/slas/contracts` with a template that does
  not exist and a source that does answers 500; with both absent it answers
  404.
- `GET /database/dump` (and v2's `/system/database-dump`) answers 200 with an
  empty body in the rig, where the daemon runs on a host without `pg_dump`.
- `GET /appliance/specs`, `POST /network/interfaces` and `GET /logs` cannot
  answer in the rig -- the mock orkestrator serves neither, and the daemon
  runs in the foreground with no log file -- so the scenarios assert the
  refusals as they come (500, 501 and 404).

v2

- `POST /connectors` answers 500 for a `urn_id` or a field `provider_id` that
  names nothing, and accepts a field with no key.
- `GET /jobs?status=bogus` answers 500.
- `POST /data-classes` and `POST /environments` answer 500 for a name that
  already exists; v1 treats it as already created.
- `PATCH /schedulers/{id}/roles` accepts a token that is neither a group nor a
  built-in.
- `GET /integrations/resolve` without `name` answers in fuego's
  `{title,status,detail}` shape rather than the v2 envelope.
- `POST /bundles` refuses any `fields` as `invalid_argument`, as v1 does,
  though the v2 contract declares them.
- `PATCH /inventories/{id}` -- a rename -- empties the inventory's resources,
  where v1's reconfiguration keeps them.
- `POST /schedulers` for a caller who already has a scheduler of their own
  answers 500; v1 answers 409.
- `GET /scheduler-tasks` lists the task every one-off run leaves behind (v1's
  `POST /scheduling/scheduler/run` makes one too, and hides it from its task
  listing), with nothing to tell it from a scheduled task and no filter to
  leave it out; the rig accumulates one per run.
- `GET /system/database-dump` answers 200 with an empty body in the rig, as
  v1 does.
- `GET /system/logs` answers 500 when there is no log file for the day; v1
  answers 404.
- `PATCH /settings/{name}` on a deployment section (`logs`, `packages`)
  answers the row with scope `deployment`, but `GET /settings/{name}` then
  says `not_found` and `GET /settings` does not list it: a deployment section
  can be written and never read back.
- `PATCH /settings/packages` from a sub-organization is accepted and lands on
  the deployment's package feed, so every install after it goes to that URL;
  v1 refuses the deployment's sections from an organization.

### Parity gaps

What v1 does that v2 cannot, each a failing v2 scenario:

| v1 feature (pcpctl) | v2 scenario |
|---|---|
| `quota get` / `quota set` | `TestQuota` |
| `inventory resources residency` (one resource overriding its inventory) | `TestInventoryResidency/resource_override` |
| `integration catalog` / `integration install <name> <version>` from the feed | `TestIntegrationCatalogue/catalogue` |
| `deployment debug-log` | `TestDeployment/debug_log` |
| `deployment interfaces` | `TestDeployment/network_interfaces` |
| `deployment license` | `TestDeployment/licence` |
| `scheduler journal` | `TestSchedulerJournal` |
| `connector capabilities` / `secrets` / `form` | `TestConnectorExtras/capabilities`, `/secrets`, `/form` |
| `backup list/get`, `restore list/get/points` | `TestBackupAndRestoreViews` |
| `integration schema` / `describe --type` | `TestIntegrationDetail/protocols` |
| `integration install --current` | `TestIntegrationDetail/install_and_make_current` |
| `secret-provider capabilities` / `secrets` / `form` | `TestSecretProviderCreate/absent` |
| `sla list` (the compliance view) | `TestSLATemplates/service_levels` |
| `deployment debug-log --enable` | `TestDeploymentExtras/debug_log` |
| `deployment specs`, `interfaces configure` | `TestDeploymentExtras/appliance` |
| `inventory sync`, `sync-period` | `TestInventoryExtras/sync` |
| `inventory secrets`, `capabilities` | `TestInventoryExtras/secrets_and_capabilities` |
| `bundle form`, `bundle secrets` | `TestBundles/form` |

Where v2 reaches the feature by another road, the v2 scenario says which:
`organization capabilities` is the session's roles read against `/roles`,
`my-groups` is `/group-memberships?user_id=`, `--organization` is a re-scoped
badge, a lookup by name is a `search=` filter, the inventory resource count
is the coverage report. Where the two sides disagree on a refusal, each
scenario asserts its own side and says so in a comment: v2 answers
`invalid_argument` where v1 answers 404 for an absent id in a body, adds a
resource under an existing urn by replacing it where v1 conflicts, validates
a subclass against the class tree where v1 does not, creates a cloud
inventory with no configuration (installing the provider's integration on the
way) where v1 refuses it, re-generates a resource's id on every edit, and lets
a badge re-scope to any organization the caller holds an effective role in,
parents included.

## The rig

`rig/qa.sh` builds pcpctl from `PCPCTL_SRC` (default: the `pcpctl` checkout
beside QA), mounts `/api/v2` on the instance, and runs `api/v1`, then
`api/v2`, then `api/v1` once more with pcpctl on v2, with one summary line
per scenario. The exit status covers the three.
