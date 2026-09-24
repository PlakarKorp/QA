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
MINIO_HOST="${MINIO_HOST:-localhost}"
S3_VERSION="${S3_VERSION:-v1.1.6}"
MIN_VERSIONS="${MIN_VERSIONS:-1}"
# What phase 2 runs. Everything, by default: most of plakman's suite is
# permission and misuse coverage, and all of it is worth running against a real
# instance rather than in process.
SUITE="${SUITE:-.}"
KEEP="${KEEP:-0}"

PLAKMAN_URL="http://localhost:$PLAKMAN_PORT"
PARENT_URL="http://localhost:$PARENT_PORT"
DB_URL="postgres://plakman:plakman@localhost:$DB_PORT/plakman"

COMPOSE=(docker compose -f "$PLAKMAN_SRC/compose.yaml" -f "$QA_ROOT/rig/compose.qa.yaml")
export QA_MOCK_PARENT="$QA_ROOT/bin/mock-parent-static"
export QA_MOCK_ORKESTRATOR="$QA_ROOT/bin/mock-orkestrator-static"
ORKESTRATOR_URL="http://localhost:${ORKESTRATOR_PORT:-18083}"

log()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
note() { printf '    %s\n' "$*"; }
die()  { printf '\n\033[31mqa: %s\033[0m\n' "$*" >&2; exit 1; }

wait_for() { # wait_for <url> <what>
  for _ in $(seq 1 120); do curl -sf -o /dev/null "$1" && return 0; sleep 1; done
  die "timed out waiting for $2 at $1"
}

# --- build -----------------------------------------------------------------

build() {
  log "building"
  ( cd "$QA_ROOT" && go build -o bin/ ./cmd/... ) || die "qa tools failed to build"
  # The mocks run in containers off a bind mount, so they have to be static and
  # built for the docker server, which is not the host on macOS.
  local target
  target="$(docker version -f '{{.Server.Os}} {{.Server.Arch}}')" || die "docker is not reachable"
  local static=(env CGO_ENABLED=0 GOOS="${target% *}" GOARCH="${target#* }" go build -ldflags='-s -w')
  ( cd "$QA_ROOT" && "${static[@]}" -o bin/mock-parent-static ./cmd/mock-parent ) \
    || die "static mock parent failed to build"
  # plakman itself: we run the binary rather than the image, because building
  # the image needs BuildKit and an SSH agent with private-repo access. This is
  # the rig's biggest compromise -- it tests a plakman, not the shipped one.
  ( cd "$PLAKMAN_SRC" && go build -o "$QA_ROOT/bin/" ./cmd/plakman ./cmd/plaklet ./cmd/cached ) \
    || die "plakman failed to build (is GOPRIVATE set, and an SSH key loaded?)"
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
  "${COMPOSE[@]}" up -d db minio backend orkestrator >/dev/null 2>&1 || die "compose up failed"
  wait_for "http://localhost:9000/minio/health/live" "minio"
  wait_for "$PARENT_URL/api/v1/pki/keys/mock-parent" "mock parent"
  wait_for "$ORKESTRATOR_URL/feed" "mock orkestrator"
  ( cd "$PLAKMAN_SRC" && sh scripts/create-buckets.sh >/dev/null 2>&1 ) || die "bucket creation failed"
  note "db :$DB_PORT   minio :9000   parent :$PARENT_PORT   orkestrator :${ORKESTRATOR_PORT:-18083}"
}

# reset wipes both halves of the instance identity: plakman's database and
# state, and the parent's memory. Wiping only one leaves a split brain -- the
# parent still holding organizations plakman has forgotten, which is what makes
# a bootstrap 'count' assertion fail for reasons that have nothing to do with
# plakman.
reset() {
  log "reset (plakman + parent)"
  plakman_stop
  docker exec plakman-db-1 psql -U plakman -d postgres -c "DROP DATABASE IF EXISTS plakman;" >/dev/null 2>&1
  docker exec plakman-db-1 psql -U plakman -d postgres -c "CREATE DATABASE plakman OWNER plakman;" >/dev/null 2>&1
  rm -rf "$STATE"; mkdir -p "$STATE/pkgs"
  "${COMPOSE[@]}" up -d --force-recreate backend >/dev/null 2>&1 || die "could not recreate the parent"
  wait_for "$PARENT_URL/api/v1/pki/keys/mock-parent" "mock parent"
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
YAML
}

plakman_start() { # plakman_start <phase>
  plakman_config
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
  disown
  wait_for "$PLAKMAN_URL/health" "plakman"
}

plakman_stop() {
  local pid; pid="$(pgrep -x plakman | head -1)"
  [ -n "$pid" ] && kill "$pid" 2>/dev/null && sleep 1
  return 0
}

# --- the suite -------------------------------------------------------------

run_suite() { # run_suite <name> <-run regexp> <outfile>
  ( cd "$PLAKMAN_SRC" && \
    PLAKMAN_DAEMON="$PLAKMAN_URL" \
    PLAKMAN_PARENT="$PARENT_URL" \
    PLAKMAN_DATABASE="$DB_URL" \
    PLAKMAN_TEST_MINIO_HOST="$MINIO_HOST" \
    PLAKMAN_REQUIRE_INFRA=1 \
    go test -tags devel -count=1 -v -timeout 35m ./tests/ -run "$2" ${4:+-skip "$4"} ) > "$3" 2>&1
  return $?
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
  "$QA_ROOT/bin/enroll" -parent "$PARENT_URL" -plakman "$PLAKMAN_URL" \
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
    "${COMPOSE[@]}" stop db minio backend >/dev/null 2>&1
    [ -n "${QA_OWNS_AGENT:-}" ] && kill "$QA_OWNS_AGENT" 2>/dev/null
  fi

  [ "$p1" -eq 0 ] && [ "$p2" -eq 0 ] && [ "$p3" -eq 0 ]
}

main "$@"
