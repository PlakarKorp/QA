#!/usr/bin/env bash
#
# One QA run: stand a plakman up, drive plakman's own suite against it, say what
# happened.
#
# The run is in two phases because the bootstrap tests are the tests *of*
# enrollment: they need a plakman that has never enrolled AND a parent that has
# never issued anything. Everything else needs the opposite -- enrolled, seeded,
# integration installed. One instance cannot be both, so the rig resets between
# them.
set -u -o pipefail

QA_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLAKMAN_SRC="${PLAKMAN_SRC:-$QA_ROOT/../../plakman}"
CORE_API_SRC="${CORE_API_SRC:-$QA_ROOT/../../core-api}"
PCPCTL_SRC="${PCPCTL_SRC:-$QA_ROOT/../../pcpctl}"

# Short on purpose: cached binds a unix socket under the state directory and
# the path limit is ~108 bytes, which a deep scratch path blows.
RUN_DIR="${RUN_DIR:-/tmp/plakman-qa}"
# The instance state is wiped between phases; the logs are the point of the run
# and must outlive that, so they live beside it rather than inside it.
STATE="$RUN_DIR/state"
LOGS="$RUN_DIR/logs"

PLAKMAN_PORT="${PLAKMAN_PORT:-18080}"
PARENT_PORT="${PARENT_PORT:-18081}"
DB_PORT="${DB_PORT:-15432}"
GARAGE_PORT="${GARAGE_PORT:-3900}"
GARAGE_ADMIN_PORT="${GARAGE_ADMIN_PORT:-3903}"
MINIO_HOST="${MINIO_HOST:-localhost}"
# The catalog offers one version per integration, the newest for the edition,
# so a pin older than that can never be installed: the default takes what the
# catalog offers, and S3_VERSION pins a run to a version it is known to list.
S3_VERSION="${S3_VERSION:-}"
MIN_VERSIONS="${MIN_VERSIONS:-1}"
# What phase 2 runs. Everything, by default: most of plakman's suite is
# permission and misuse coverage, and all of it is worth running against a real
# instance rather than in process.
SUITE="${SUITE:-.}"
KEEP="${KEEP:-0}"

PLAKMAN_URL="http://localhost:$PLAKMAN_PORT"
PARENT_URL="http://localhost:$PARENT_PORT"
DB_URL="postgres://plakman:plakman@localhost:$DB_PORT/plakman"

# Our own project name, so the rig never shares containers or volumes with a
# stack someone runs from the plakman checkout itself.
COMPOSE=(docker compose -p qa-plakman -f "$PLAKMAN_SRC/compose.yaml" -f "$QA_ROOT/rig/compose.qa.yaml")
export QA_DB_PORT="$DB_PORT" QA_GARAGE_PORT="$GARAGE_PORT" QA_GARAGE_ADMIN_PORT="$GARAGE_ADMIN_PORT"
# core-api's compose interpolates SSH_AUTH_SOCK before our override drops the
# mount that uses it, so it only has to be set.
PARENT=(env SSH_AUTH_SOCK="${SSH_AUTH_SOCK:-/dev/null}" docker compose -p qa-core-api -f "$CORE_API_SRC/compose.yaml" -f "$QA_ROOT/rig/compose.core-api.yaml")
export QA_CORE_API_CONFIG="$QA_ROOT/rig/core-api.qa.yaml"
MAILPIT_URL="http://localhost:18025"
export QA_MOCK_ORKESTRATOR="$QA_ROOT/bin/mock-orkestrator-static"
ORKESTRATOR_URL="http://localhost:${ORKESTRATOR_PORT:-18083}"

log()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
note() { printf '    %s\n' "$*"; }
die()  { printf '\n\033[31mqa: %s\033[0m\n' "$*" >&2; exit 1; }

wait_for() { # wait_for <url> <what> [seconds]
  for _ in $(seq 1 "${3:-120}"); do curl -sf -o /dev/null "$1" && return 0; sleep 1; done
  die "timed out waiting for $2 at $1"
}

# --- build -----------------------------------------------------------------

build() {
  log "building"
  ( cd "$QA_ROOT" && go build -o bin/ ./cmd/... ) || die "qa tools failed to build"
  # The mock orkestrator runs in a container off a bind mount, so it has to be
  # static and built for the docker server, which is not the host on macOS.
  local target
  target="$(docker version -f '{{.Server.Os}} {{.Server.Arch}}')" || die "docker is not reachable"
  local static=(env CGO_ENABLED=0 GOOS="${target% *}" GOARCH="${target#* }" go build -ldflags='-s -w')
  # plakman itself: we run the binary rather than the image, because building
  # the image needs BuildKit and an SSH agent with private-repo access. This is
  # the rig's biggest compromise -- it tests a plakman, not the shipped one.
  ( cd "$PLAKMAN_SRC" && go build -o "$QA_ROOT/bin/" ./cmd/plakman ./cmd/plaklet ./cmd/cached ) \
    || die "plakman failed to build (is GOPRIVATE set, and an SSH key loaded?)"
  # The v1 API scenarios drive the instance through pcpctl, the way a person
  # or a script does, so the contract under test is the one pcpctl speaks.
  ( cd "$PCPCTL_SRC" && go build -o "$QA_ROOT/bin/pcpctl" ./cmd/pcpctl ) \
    || die "pcpctl failed to build (is PCPCTL_SRC a pcpctl checkout?)"
  # The daemon proxies release-feed, update and network calls to an orkestrator
  # and answers 500 without one, which is a failure about the rig rather than
  # about plakman. plakman ships the mock; we just run it.
  ( cd "$PLAKMAN_SRC" && "${static[@]}" \
      -o "$QA_ROOT/bin/mock-orkestrator-static" ./cmd/mock-orkestrator ) \
    || die "mock orkestrator failed to build"
}

# --- infrastructure --------------------------------------------------------

infra_up() {
  log "infrastructure"
  docker network inspect plakario_network >/dev/null 2>&1 || docker network create plakario_network >/dev/null
  "${COMPOSE[@]}" up -d --remove-orphans db garage orkestrator >/dev/null 2>&1 || die "compose up failed"
  "${PARENT[@]}" up -d --build db mailpit api >/dev/null 2>&1 || die "core-api compose up failed"
  wait_for "http://localhost:$GARAGE_ADMIN_PORT/health" "garage"
  # Long: the api container compiles core-api on start.
  wait_for "$PARENT_URL/health/ready" "core-api" 600
  wait_for "$ORKESTRATOR_URL/feed" "mock orkestrator"
  # plakman's compose moved from MinIO to garage, and its bucket setup
  # (scripts/setup-garage.sh) now goes through the stack's vault and sftp
  # containers, which the rig does not run. The buckets the data-path e2e
  # tests want are therefore not provisioned here; the API scenarios need none.
  note "db :$DB_PORT   garage :$GARAGE_PORT   parent :$PARENT_PORT   orkestrator :${ORKESTRATOR_PORT:-18083}"
}

# reset wipes both halves of the instance identity: plakman's database and
# state, and the parent's memory. Wiping only one leaves a split brain -- the
# parent still holding organizations plakman has forgotten, which is what makes
# a bootstrap 'count' assertion fail for reasons that have nothing to do with
# plakman.
reset() {
  log "reset (plakman + parent)"
  plakman_stop
  "${COMPOSE[@]}" exec -T db psql -U plakman -d postgres -c "DROP DATABASE IF EXISTS plakman;" >/dev/null 2>&1
  "${COMPOSE[@]}" exec -T db psql -U plakman -d postgres -c "CREATE DATABASE plakman OWNER plakman;" >/dev/null 2>&1
  rm -rf "$STATE"; mkdir -p "$STATE/pkgs"
  # core-api migrates and seeds its dev user on boot, so a fresh database and a
  # restart give a parent that has never issued anything.
  "${PARENT[@]}" stop api >/dev/null 2>&1
  "${PARENT[@]}" exec -T db psql -U plakar -d postgres -c "DROP DATABASE IF EXISTS plakar WITH (FORCE);" >/dev/null 2>&1
  "${PARENT[@]}" exec -T db psql -U plakar -d postgres -c "CREATE DATABASE plakar OWNER plakar;" >/dev/null 2>&1
  curl -sf -X DELETE "$MAILPIT_URL/api/v1/messages" >/dev/null || die "could not empty the mailbox"
  "${PARENT[@]}" start api >/dev/null 2>&1 || die "could not restart core-api"
  wait_for "$PARENT_URL/health/ready" "core-api" 600
}

# --- plakman ---------------------------------------------------------------

plakman_config() {
  cat > "$STATE/plakman.yaml" <<YAML
services:
  http:
    listen:
      address: :$PLAKMAN_PORT
  database:
    url: $DB_URL
  plakman:
    parentUrl: $PARENT_URL
    stateDirectory: $STATE/
  scheduler:
    plakmanUrl: $PLAKMAN_URL
  executor:
    pkgDir: $STATE/pkgs/
    plakmanUrl: $PLAKMAN_URL
    plakletBin: $QA_ROOT/bin/plaklet
    cachedBin: $QA_ROOT/bin/cached
  journal:
    dbStorage: true
    logPath: $STATE/journal.log
  janitor:
    plakmanUrl: $PLAKMAN_URL
  log:
    level: DEBUG
api:
  v1:
    mode:
      plakman: true
      plakario: false
      plakarui: true
    cors: true
  # The v2 scenarios need the surface mounted; deployments leave it off.
  v2:
    enabled: true
YAML
}

plakman_start() { # plakman_start <phase>
  plakman_config
  # A plakman of someone else's on our port would answer our health check and
  # take the whole run, so refuse rather than test the wrong daemon.
  if curl -sf -o /dev/null "$PLAKMAN_URL/health"; then
    die "something already answers on $PLAKMAN_URL; pick another PLAKMAN_PORT"
  fi
  # The executor refuses to initialise without an agent, so the rig owns one.
  if [ -z "${SSH_AUTH_SOCK:-}" ]; then
    eval "$(ssh-agent -s)" >/dev/null
    export QA_OWNS_AGENT="${SSH_AGENT_PID:-}"
  fi
  nohup env \
    PLAKMAN_RELEASE_FEED_URL="$ORKESTRATOR_URL/feed" \
    PLAKMAN_RELEASE_FEED_CACHE_TTL=0 \
    PLAKMAN_UPDATE_URL="$ORKESTRATOR_URL/update" \
    PLAKMAN_UPDATE_TASK_STATUS_URL="$ORKESTRATOR_URL/task_status" \
    PLAKMAN_NET_INTERFACES_URL="$ORKESTRATOR_URL/net/interfaces" \
    PLAKMAN_NET_CONFIGURE_URL="$ORKESTRATOR_URL/net/configure" \
    "$QA_ROOT/bin/plakman" -config "$STATE/plakman.yaml" -foreground \
    > "$LOGS/plakman.${1:-run}.log" 2>&1 &
  echo $! > "$RUN_DIR/plakman.pid"
  disown
  wait_for "$PLAKMAN_URL/health" "plakman"
}

# plakman_stop stops the daemon this run started and nothing else: a developer
# machine runs other plakmans, and a bare pgrep once stopped one of those and
# left ours holding the port.
plakman_stop() {
  if [ -f "$RUN_DIR/plakman.pid" ]; then
    kill "$(cat "$RUN_DIR/plakman.pid")" 2>/dev/null
    rm -f "$RUN_DIR/plakman.pid"
  fi
  pkill -f "^$QA_ROOT/bin/plakman " 2>/dev/null
  for _ in $(seq 1 30); do
    curl -sf -o /dev/null "$PLAKMAN_URL/health" || return 0
    sleep 1
  done
  die "the plakman on $PLAKMAN_URL did not stop"
}

# --- the suite -------------------------------------------------------------

run_suite() { # run_suite <name> <-run regexp> <outfile>
  ( cd "$PLAKMAN_SRC" && \
    PLAKMAN_DAEMON="$PLAKMAN_URL" \
    PLAKMAN_PARENT="$PARENT_URL" \
    PLAKMAN_MAILPIT="$MAILPIT_URL" \
    PLAKMAN_DATABASE="$DB_URL" \
    PLAKMAN_TEST_MINIO_HOST="$MINIO_HOST" \
    PLAKMAN_REQUIRE_INFRA=1 \
    go test -tags devel -count=1 -v -timeout 35m ./tests/ -run "$2" ${4:+-skip "$4"} ) > "$3" 2>&1
  return $?
}

# run_api runs one API scenario package against the standing instance and
# prints one line per scenario, subtests included, so the summary names the
# contract that broke rather than the file it lives in. The label is what
# the summary calls the phase; the api is the wire pcpctl speaks in it.
run_api() { # run_api <package> <label> <pcpctl api> <outfile>
  ( cd "$QA_ROOT" && \
    PLAKMAN_URL="$PLAKMAN_URL" PLAKMAN_PARENT="$PARENT_URL" \
    PCPCTL_BIN="$QA_ROOT/bin/pcpctl" PCPCTL_API="$3" \
    go test "./api/$1/..." -count=1 -v -timeout 20m ) > "$4" 2>&1
  local rc=$?
  grep -E '^\s*--- (PASS|FAIL|SKIP)' "$4" | sed -E 's/^ *//; s/^--- //; s/ \(.*\)$//' \
    | awk -v v="$2" '{printf "    %-5s %s/%s\n", $1, v, $2}'
  if [ "$rc" -ne 0 ] && ! grep -qE '^\s*--- FAIL' "$4"; then
    # Nothing ran: a build failure or a harness that could not connect.
    sed 's/^/    /' "$4" | tail -20
  fi
  return $rc
}

# --- the run ---------------------------------------------------------------

main() {
  mkdir -p "$STATE" "$LOGS"
  build
  infra_up

  # Phase 1: enrollment, on an instance that has never enrolled.
  reset
  plakman_start bootstrap
  log "bootstrap (pristine instance)"
  run_suite bootstrap 'TestBootstrap$' "$LOGS/bootstrap.out"
  p1=$?
  grep -E '^\s+--- (PASS|FAIL)' "$LOGS/bootstrap.out" | sed 's/^/    /' || true

  # Everything else shares one enrolled, seeded instance.
  reset
  plakman_start main
  log "enroll and seed"
  "$QA_ROOT/bin/enroll" -plakman "$PLAKMAN_URL" -mailpit "$MAILPIT_URL" \
    | sed 's/^/    /' || die "enrollment failed"
  "$QA_ROOT/bin/seed" -parent "$PARENT_URL" -plakman "$PLAKMAN_URL" \
    -s3-version "$S3_VERSION" -retention-min-versions "$MIN_VERSIONS" \
    | sed 's/^/    /' || die "seeding failed"

  # Ours first, deliberately. plakman's suite mutates deployment-scoped
  # settings and does not put them back -- organization_settings_test and
  # settings_rehome_test both leave the packages repository pointing at
  # mirror.example -- which is invisible in process, where every run gets a
  # fresh database, and poisons anything sharing a long-lived instance. Running
  # ours afterwards meant reading a 502 from a feed a previous test had broken.
  log "coverage scenarios"
  ( cd "$QA_ROOT" && \
    PLAKMAN_URL="$PLAKMAN_URL" PLAKMAN_PARENT="$PARENT_URL" \
    go test ./scenarios/... -count=1 -v ) > "$LOGS/scenarios.out" 2>&1
  p2=$?
  grep -E '^--- (PASS|FAIL|SKIP)' "$LOGS/scenarios.out" | sed 's/^/    /' || true

  # The API contract, service by service: v1 as pcpctl drives it, then v2
  # mirroring every v1 scenario by name. A v2 scenario that fails names the
  # v1 feature it mirrors, so a red line here is either a broken contract or
  # a parity gap. See api/README.md.
  log "api v1 scenarios (pcpctl)"
  run_api v1 v1 v1 "$LOGS/api-v1.out"
  p4=$?
  log "api v2 scenarios (parity)"
  run_api v2 v2 v2 "$LOGS/api-v2.out"
  p5=$?
  # The v1 scenarios once more, with pcpctl speaking v2: the same commands,
  # the same exit codes, over pcpctl's v2 wire. A feature v2 has no route
  # for is asserted as pcpctl's own refusal, so a red line here is the wire
  # disagreeing with v1, not a parity gap.
  log "api v1 scenarios (pcpctl on v2)"
  run_api v1 pcpctl-v2 v2 "$LOGS/api-pcpctl-v2.out"
  p6=$?

  log "plakman suite"
  run_suite suite "$SUITE" "$LOGS/suite.out" 'TestBootstrap$'
  p3=$?
  grep -E '^--- (PASS|FAIL)' "$LOGS/suite.out" | sed 's/^/    /' || true

  # --- report ---
  log "result"
  pass=$(cat "$LOGS"/*.out | grep -cE '^--- PASS' || true)
  fail=$(cat "$LOGS"/*.out | grep -cE '^--- FAIL' || true)
  printf '    passed   %s\n    failed   %s\n' "$pass" "$fail"
  printf '    logs     %s\n' "$LOGS"

  # Coverage, from the daemon's own log: what it mounted at startup versus what
  # it answered. A green run says what we tested; only the untouched list says
  # what we are still guessing at.
  printf '\n'
  "$QA_ROOT/bin/ledger" "$LOGS"/plakman.*.log | sed 's/^/    /'
  "$QA_ROOT/bin/ledger" "$LOGS"/plakman.*.log > "$LOGS/ledger.txt" 2>&1
  if [ "$fail" -gt 0 ]; then
    printf '\n    failing:\n'
    cat "$LOGS"/*.out | grep -E '^\s*--- FAIL' | sed 's/^/      /'
  fi

  if [ "$KEEP" = "1" ]; then
    printf '\n    kept running: plakman %s, parent %s (KEEP=1)\n' "$PLAKMAN_URL" "$PARENT_URL"
  else
    log "teardown"
    plakman_stop
    "${COMPOSE[@]}" stop db garage orkestrator >/dev/null 2>&1
    "${PARENT[@]}" stop >/dev/null 2>&1
    [ -n "${QA_OWNS_AGENT:-}" ] && kill "$QA_OWNS_AGENT" 2>/dev/null
  fi

  [ "$p1" -eq 0 ] && [ "$p2" -eq 0 ] && [ "$p3" -eq 0 ] && [ "$p4" -eq 0 ] && [ "$p5" -eq 0 ] && [ "$p6" -eq 0 ]
}

main "$@"
