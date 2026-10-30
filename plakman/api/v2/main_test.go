// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

// Package apiv2 mirrors api/v1 on the v2 API, scenario for scenario: every
// Test function in api/v1 has one of the same name here, driving the same
// feature through v2's own contract. The shapes differ and one v1 command is
// often several v2 calls; what must hold is that the feature is reachable.
//
// A v2 scenario that cannot reach its feature fails through gap(), naming
// the v1 feature it mirrors, so `make qa` shows parity gaps as red lines
// rather than as silence. TestParity fails when a v1 scenario has no mirror
// at all. See api/README.md.
package apiv2

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
)

var (
	inst  *harness.Instance
	v2    *harness.V2
	ready bool
)

func TestMain(m *testing.M) {
	flag.Parse()
	if err := connect(); err != nil {
		fmt.Fprintln(os.Stderr, "SKIP api/v2: no instance:", err)
	} else {
		ready = true
	}
	os.Exit(m.Run())
}

func connect() error {
	var err error
	if inst, err = harness.Connect(); err != nil {
		return err
	}
	v2, err = harness.LoginV2(inst)
	return err
}

func requireInstance(t *testing.T) {
	t.Helper()
	if !ready {
		t.Skip("no instance (make instance, then make api-v2)")
	}
}

// missing is a well-formed id that names nothing.
const missing = "00000000-0000-4000-8000-000000000000"

// gap fails the scenario because v2 offers no way to do what v1 does. The
// message names the v1 feature, which is what a reader of the summary needs.
func gap(t *testing.T, v1Feature string) {
	t.Helper()
	t.Fatalf("parity gap: v2 cannot %s", v1Feature)
}

// finding is a behaviour the scenario cannot assert as a contract, as in
// api/v1: skipped, with the finding in the name, and listed in api/README.md.
func finding(t *testing.T, what string) {
	t.Helper()
	t.Skip("finding: " + what)
}
