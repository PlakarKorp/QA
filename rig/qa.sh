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
PLAKMAN_SRC="${PLAKMAN_SRC:-$QA_ROOT/../plakman}"

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
KEEP="${KEEP:-0}"

PLAKMAN_URL="http://localhost:$PLAKMAN_PORT"
PARENT_URL="http://localhost:$PARENT_PORT"
DB_URL="postgres://plakman:plakman@localhost:$DB_PORT/plakman"

COMPOSE=(docker compose -f "$PLAKMAN_SRC/compose.yaml" -f "$QA_ROOT/rig/compose.qa.yaml")
export QA_MOCK_PARENT="$QA_ROOT/bin/mock-parent-static"

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
  # The mock parent runs in a container off a bind mount, so it has to be static.
  ( cd "$QA_ROOT" && CGO_ENABLED=0 go build -ldflags='-s -w' -o bin/mock-parent-static ./cmd/mock-parent ) \
    || die "static mock parent failed to build"
  # plakman itself: we run the binary rather than the image, because building
  # the image needs BuildKit and an SSH agent with private-repo access. This is
  # the rig's biggest compromise -- it tests a plakman, not the shipped one.
  ( cd "$PLAKMAN_SRC" && go build -o "$QA_ROOT/bin/" ./cmd/plakman ./cmd/plaklet ./cmd/cached ) \
    || die "plakman failed to build (is GOPRIVATE set, and an SSH key loaded?)"
}

# --- infrastructure --------------------------------------------------------

infra_up() {
  log "infrastructure"
  docker network inspect plakario_network >/dev/null 2>&1 || docker network create plakario_network >/dev/null
  "${COMPOSE[@]}" up -d db minio backend >/dev/null 2>&1 || die "compose up failed"
  wait_for "http://localhost:9000/minio/health/live" "minio"
  wait_for "$PARENT_URL/api/v1/pki/keys/mock-parent" "mock parent"
  ( cd "$PLAKMAN_SRC" && sh scripts/create-buckets.sh >/dev/null 2>&1 ) || die "bucket creation failed"
  note "db :$DB_PORT   minio :9000   parent :$PARENT_PORT"
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
  nohup "$QA_ROOT/bin/plakman" -config "$STATE/plakman.yaml" -foreground \
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
    go test -tags devel -count=1 -v -timeout 35m ./tests/ -run "$2" ) > "$3" 2>&1
  return $?
}

# --- the run ---------------------------------------------------------------

main() {
  mkdir -p "$STATE" "$LOGS"
  build
  infra_up

  # Phase 1: enrollment, on an instance that has never enrolled.
  reset
  plakman_start phase1
  log "phase 1: bootstrap (pristine instance)"
  run_suite bootstrap 'TestBootstrap$' "$LOGS/phase1.out"
  p1=$?
  grep -E '^\s+--- (PASS|FAIL)' "$LOGS/phase1.out" | sed 's/^/    /' || true

  # Phase 2: everything that needs an enrolled, seeded instance.
  reset
  plakman_start phase2
  log "phase 2: enroll and seed"
  "$QA_ROOT/bin/enroll" -parent "$PARENT_URL" -plakman "$PLAKMAN_URL" \
    | sed 's/^/    /' || die "enrollment failed"
  "$QA_ROOT/bin/seed" -parent "$PARENT_URL" -plakman "$PLAKMAN_URL" \
    -s3-version "$S3_VERSION" -retention-min-versions "$MIN_VERSIONS" \
    | sed 's/^/    /' || die "seeding failed"

  log "phase 2: scenarios"
  run_suite scenarios \
    'TestAccountMe|TestBadges$|TestLogin$|TestCreateOrganization$|TestE2E' \
    "$LOGS/phase2.out"
  p2=$?
  grep -E '^--- (PASS|FAIL)' "$LOGS/phase2.out" | sed 's/^/    /' || true

  # --- report ---
  log "result"
  pass=$(cat "$LOGS"/phase*.out | grep -cE '^--- PASS' || true)
  fail=$(cat "$LOGS"/phase*.out | grep -cE '^--- FAIL' || true)
  printf '    passed   %s\n    failed   %s\n' "$pass" "$fail"
  printf '    logs     %s\n' "$LOGS"
  if [ "$fail" -gt 0 ]; then
    printf '\n    failing:\n'
    cat "$LOGS"/phase*.out | grep -E '^\s*--- FAIL' | sed 's/^/      /'
  fi

  if [ "$KEEP" = "1" ]; then
    printf '\n    kept running: plakman %s, parent %s (KEEP=1)\n' "$PLAKMAN_URL" "$PARENT_URL"
  else
    log "teardown"
    plakman_stop
    "${COMPOSE[@]}" stop db minio backend >/dev/null 2>&1
    [ -n "${QA_OWNS_AGENT:-}" ] && kill "$QA_OWNS_AGENT" 2>/dev/null
  fi

  [ "$p1" -eq 0 ] && [ "$p2" -eq 0 ]
}

main "$@"
