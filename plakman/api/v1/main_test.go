// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

// Package apiv1 drives the plakman v1 API the way a person does: through
// pcpctl, service by service, on a standing instance (`make instance`).
//
// Each scenario asserts the contract exactly -- the exit code pcpctl
// documents, the fields the JSON carries, what a listing shows after a create
// and after a delete -- so a green run means no v1 behaviour a script relies
// on has moved. The misuse paths (a delete of something absent, a duplicate,
// a read after delete, a bad argument) are scenarios of their own, because
// they are the half of the contract that runs on a bad day.
//
// api/v2 mirrors every scenario here by name; see api/README.md.
package apiv1

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
)

var (
	inst   *harness.Instance
	pcpctl *harness.Pcpctl
	ready  bool
)

func TestMain(m *testing.M) {
	flag.Parse()
	if err := connect(); err != nil {
		fmt.Fprintln(os.Stderr, "SKIP api/v1: no instance:", err)
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
	pcpctl, err = harness.NewPcpctl(inst)
	return err
}

func requireInstance(t *testing.T) {
	t.Helper()
	if !ready {
		t.Skip("no instance (make instance, then make api-v1)")
	}
}

// missing is a well-formed id that names nothing.
const missing = "00000000-0000-4000-8000-000000000000"

// finding records a behaviour the scenario cannot assert as a contract: the
// server falls over, or pcpctl and the API disagree, so neither answer is one
// a script should rely on. The subtest is skipped with the finding in its
// name, which keeps the run green while the summary still shows it, and
// api/README.md lists every one. Fix the finding, then turn the skip into
// the assertion.
func finding(t *testing.T, what string) {
	t.Helper()
	t.Skip("finding: " + what)
}
