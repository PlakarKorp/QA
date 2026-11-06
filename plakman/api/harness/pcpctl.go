// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CommandTimeout bounds one invocation: a connector test against an endpoint
// that never answers must not hang the whole suite.
const CommandTimeout = 3 * time.Minute

// The exit codes pcpctl documents. They are part of its interface and a
// scenario asserts them by name.
const (
	ExitOK           = 0
	ExitError        = 1 // any other failure
	ExitUsage        = 2 // the command line is wrong
	ExitNotFound     = 3 // 404 or 410, or a name that matched nothing
	ExitConflict     = 4 // 409
	ExitUnauthorized = 5 // no key, a rejected one, 401 or 403
)

// Pcpctl runs the pcpctl binary against one instance as one API key. It is
// the whole of what the v1 scenarios touch: they never speak HTTP themselves,
// so what they assert is the contract as pcpctl presents it to a person.
type Pcpctl struct {
	Bin    string
	Server string
	APIKey string

	// Organization pins every command to an organization below the key's
	// own, as --organization would; empty means the key's.
	Organization string

	// Stdin is fed to the command, for what pcpctl reads there rather than
	// from a flag: a password, a CSV to import.
	Stdin string

	// config is a file that does not exist, so the developer's own
	// ~/.config/pcpctl/config.json never leaks into a run.
	config string
}

// API is the wire the scenarios run pcpctl on: PCPCTL_API in the
// environment, which the rig sets for the phase that runs the v1 scenarios
// over v2, else v1. pcpctl reads the same variable.
func API() string {
	if api := os.Getenv("PCPCTL_API"); api != "" {
		return api
	}
	return "v1"
}

// OnV2 reports whether the scenarios run pcpctl on the v2 wire.
func OnV2() bool { return API() == "v2" }

// NewPcpctl locates the binary: $PCPCTL_BIN, else bin/pcpctl under the module
// root, which is where the rig builds it. A missing binary is an error the
// caller turns into a skip.
func NewPcpctl(inst *Instance) (*Pcpctl, error) {
	bin := os.Getenv("PCPCTL_BIN")
	if bin == "" {
		root, err := ModuleRoot()
		if err != nil {
			return nil, err
		}
		bin = filepath.Join(root, "bin", "pcpctl")
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("no pcpctl at %s (set PCPCTL_BIN, or run make api-v1): %w", bin, err)
	}
	dir, err := os.MkdirTemp("", "pcpctl-qa-")
	if err != nil {
		return nil, err
	}
	return &Pcpctl{
		Bin:    bin,
		Server: inst.PlakmanURL,
		APIKey: inst.APIKey,
		config: filepath.Join(dir, "config.json"),
	}, nil
}

// In returns a copy pinned to another organization, by name or id.
func (p *Pcpctl) In(organization string) *Pcpctl {
	c := *p
	c.Organization = organization
	return &c
}

// WithStdin returns a copy whose commands read the text on standard input.
func (p *Pcpctl) WithStdin(text string) *Pcpctl {
	c := *p
	c.Stdin = text
	return &c
}

// Result is what one invocation left behind.
type Result struct {
	Args   []string
	Code   int
	Stdout string
	Stderr string
}

// String renders the invocation for a failure message.
func (r Result) String() string {
	return fmt.Sprintf("pcpctl %s -> exit %d\nstdout: %s\nstderr: %s",
		strings.Join(r.Args, " "), r.Code, strings.TrimSpace(r.Stdout), strings.TrimSpace(r.Stderr))
}

// Run invokes pcpctl and returns whatever happened, exit code included; the
// caller decides what it expected.
func (p *Pcpctl) Run(t testing.TB, args ...string) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Bin, args...)
	cmd.Env = append(os.Environ(),
		"PCPCTL_SERVER="+p.Server,
		"PCPCTL_API_KEY="+p.APIKey,
		"PCPCTL_CONFIG="+p.config,
		"PCPCTL_ORGANIZATION="+p.Organization,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if p.Stdin != "" {
		cmd.Stdin = strings.NewReader(p.Stdin)
	}

	started := time.Now()
	err := cmd.Run()
	res := Result{Args: args, Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.Code = exit.ExitCode()
		if ctx.Err() != nil {
			t.Fatalf("pcpctl %s did not finish within %s", strings.Join(args, " "), CommandTimeout)
		}
	default:
		t.Fatalf("pcpctl %s could not run: %v", strings.Join(args, " "), err)
	}
	t.Logf("pcpctl %s -> %d (%s)", strings.Join(args, " "), res.Code, time.Since(started).Round(time.Millisecond))
	return res
}

// OK runs a command that must succeed and returns its standard output.
func (p *Pcpctl) OK(t testing.TB, args ...string) string {
	t.Helper()
	res := p.Run(t, args...)
	if res.Code != ExitOK {
		t.Fatalf("expected success\n%s", res)
	}
	return res.Stdout
}

// JSON runs a command with -o json, requires success, and decodes the output.
// The flag goes before a "--", after which everything is an argument.
func (p *Pcpctl) JSON(t testing.TB, out any, args ...string) {
	t.Helper()
	res := p.Run(t, withOutput(args, "json")...)
	if res.Code != ExitOK {
		t.Fatalf("expected success\n%s", res)
	}
	if err := json.Unmarshal([]byte(res.Stdout), out); err != nil {
		t.Fatalf("pcpctl %s: output is not the JSON expected: %v\n%s",
			strings.Join(args, " "), err, res.Stdout)
	}
}

// withOutput adds -o <format> to the arguments, before a "--" when there is
// one.
func withOutput(args []string, format string) []string {
	for i, a := range args {
		if a == "--" {
			out := make([]string, 0, len(args)+2)
			out = append(out, args[:i]...)
			out = append(out, "-o", format)
			return append(out, args[i:]...)
		}
	}
	return append(args, "-o", format)
}

// Fails runs a command that must fail with exactly this exit code, since the
// code is the contract a script branches on. It returns the result so a
// scenario can also check what was said.
func (p *Pcpctl) Fails(t testing.TB, code int, args ...string) Result {
	t.Helper()
	res := p.Run(t, args...)
	if res.Code != code {
		t.Fatalf("expected exit %d\n%s", code, res)
	}
	return res
}

// NotOnV2 is a feature v1 has and v2 does not. On the v2 wire it runs the
// command and asserts pcpctl's own refusal -- exit 1, saying the feature is
// not available on API v2, which is the contract there -- and answers true
// so the scenario skips the rest. On v1 it runs nothing and answers false.
func (p *Pcpctl) NotOnV2(t testing.TB, args ...string) bool {
	t.Helper()
	if !OnV2() {
		return false
	}
	res := p.Fails(t, ExitError, args...)
	if !strings.Contains(res.Stderr, "not available on API v2") {
		t.Fatalf("expected pcpctl to say the feature is not available on API v2\n%s", res)
	}
	return true
}
