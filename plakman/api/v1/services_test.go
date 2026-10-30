// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv1

import (
	"strings"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

// TestIntegrationCatalogue: the rig installs s3, so it is installed, current,
// available in the catalogue, and readable by name; what is not is a
// not-found.
func TestIntegrationCatalogue(t *testing.T) {
	requireInstance(t)

	var installed page[struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Version   string `json:"version"`
		IsCurrent bool   `json:"is_current"`
	}]
	pcpctl.JSON(t, &installed, "integration", "installed")
	var s3ID string
	for _, i := range installed.Items {
		if i.Name == "s3" {
			s3ID = i.ID
			require.True(t, i.IsCurrent, "the one installed version is current")
		}
	}
	require.NotEmpty(t, s3ID, "the rig installs s3")

	var s3 struct {
		Name      string   `json:"name"`
		Supports  []string `json:"supports"`
		Available []struct {
			Version string `json:"version"`
		} `json:"available"`
		Installed []struct {
			ID string `json:"id"`
		} `json:"installed"`
	}
	pcpctl.JSON(t, &s3, "integration", "get", "s3")
	require.Equal(t, "s3", s3.Name)
	require.Contains(t, s3.Supports, "store")
	require.NotEmpty(t, s3.Available)
	require.Len(t, s3.Installed, 1)
	require.Equal(t, s3ID, s3.Installed[0].ID)

	var catalog page[struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}]
	pcpctl.JSON(t, &catalog, "integration", "catalog", "s3")
	require.Len(t, catalog.Items, 1)
	require.Equal(t, s3.Available[0].Version, catalog.Items[0].Version)

	var all page[struct {
		Name string `json:"name"`
	}]
	pcpctl.JSON(t, &all, "integration", "list")
	require.Greater(t, all.Total, 1, "the catalogue offers more than what is installed")

	t.Run("absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "integration", "get", "nope")
		pcpctl.Fails(t, harness.ExitNotFound, "integration", "uninstall", missing, "--yes")
		pcpctl.Fails(t, harness.ExitNotFound, "integration", "set-current", missing)
	})
	t.Run("install what the catalogue does not offer", func(t *testing.T) {
		finding(t, "POST /integrations/installed with a name the catalogue does not offer answers 500; it should be a 404")
		pcpctl.Fails(t, harness.ExitNotFound, "integration", "install", "nope", "v0.0.0")
	})
	t.Run("install what is installed", func(t *testing.T) {
		// Idempotent: the same version again is a success, and nothing is
		// installed twice.
		before := installed.Total
		pcpctl.OK(t, "integration", "install", "s3", s3.Available[0].Version)
		pcpctl.JSON(t, &installed, "integration", "installed")
		require.Equal(t, before, installed.Total)
	})
	t.Run("uninstall without --yes", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "integration", "uninstall", s3ID)
		pcpctl.OK(t, "integration", "get", "s3")
	})
}

// TestSecretProviders: none are seeded, and every read of one that does not
// exist is a not-found rather than anything else.
func TestSecretProviders(t *testing.T) {
	requireInstance(t)

	var vaults page[map[string]any]
	pcpctl.JSON(t, &vaults, "secret-provider", "list")
	require.Equal(t, len(vaults.Items), vaults.Total)

	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "get", harness.Unique("no-such-vault"))
	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "delete", missing, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "test", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "update", missing, "--name", "ghost")
}

// TestBundles: the bundle reads and the refusals; creating one through pcpctl
// cannot succeed today, which is a finding rather than a contract.
func TestBundles(t *testing.T) {
	requireInstance(t)

	var bundles page[map[string]any]
	pcpctl.JSON(t, &bundles, "bundle", "list")
	require.Equal(t, len(bundles.Items), bundles.Total)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	pcpctl.JSON(t, &bundles, "bundle", "list", "--inventory", inv.ID)
	require.Empty(t, bundles.Items, "a new inventory confines no bundle")

	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "delete", missing, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "list", "--inventory", missing)
	pcpctl.Fails(t, harness.ExitError, "bundle", "create", "--integration", "s3", "--class", "object-storage")

	t.Run("create", func(t *testing.T) {
		finding(t, "pcpctl requires a --field on bundle create, and the v1 API refuses any ('bundle fields are not supported, use the form'): no bundle can be created through pcpctl")
		var b struct {
			ID string `json:"id"`
		}
		pcpctl.JSON(t, &b, "bundle", "create", "--integration", "s3", "--class", "object-storage", "--field", "root=/backups")
		pcpctl.OK(t, "bundle", "delete", b.ID, "--yes")
	})
}

// TestSLA: nothing is contracted on a fresh instance, the reads answer their
// empty pages, and a contract or template that does not exist is a
// not-found. pcpctl offers no template create, so the contract path is
// covered by its refusals.
func TestSLA(t *testing.T) {
	requireInstance(t)

	var slas page[map[string]any]
	pcpctl.JSON(t, &slas, "sla", "list")
	require.Equal(t, len(slas.Items), slas.Total)
	var templates []map[string]any
	pcpctl.JSON(t, &templates, "sla", "templates", "list")
	var contracts []map[string]any
	pcpctl.JSON(t, &contracts, "sla", "contracts", "list")
	var resources page[map[string]any]
	pcpctl.JSON(t, &resources, "sla", "resources")

	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "delete", missing, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "update", missing, "--name", "ghost")
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "contracts", "create", missing, missing)
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "contracts", "delete", missing, "--yes")
	pcpctl.Fails(t, harness.ExitUsage, "sla", "contracts", "create", missing)
}

// TestScheduler: every organization has a default scheduler from birth, with
// no tasks, an empty access list and roles, and nothing planned.
func TestScheduler(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	var s struct {
		ID      string `json:"id"`
		Actor   string `json:"actor"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Tasks   []any  `json:"tasks"`
	}
	in.JSON(t, &s, "scheduler", "get")
	require.NotEmpty(t, s.ID)
	require.Equal(t, "default", s.Actor)
	require.True(t, s.Enabled)
	require.Empty(t, s.Tasks)

	var users page[map[string]any]
	in.JSON(t, &users, "scheduler", "users")
	require.Empty(t, users.Items, "nobody has a scheduler of their own yet")

	var roles struct {
		CanRead    []string `json:"can_read"`
		CanExecute []string `json:"can_execute"`
		CanWrite   []string `json:"can_write"`
		CanOwn     []string `json:"can_own"`
	}
	in.JSON(t, &roles, "scheduler", "roles")
	require.Empty(t, roles.CanRead)

	var planned, nearby []any
	in.JSON(t, &planned, "scheduler", "planned")
	require.Empty(t, planned)
	in.JSON(t, &nearby, "scheduler", "nearby")
	require.Empty(t, nearby)

	var tasks page[any]
	in.JSON(t, &tasks, "task", "list")
	require.Empty(t, tasks.Items)

	in.Fails(t, harness.ExitNotFound, "task", "run", missing)
	in.Fails(t, harness.ExitNotFound, "task", "delete", missing, "--yes")
	in.Fails(t, harness.ExitError, "scheduler", "delete")
}

// TestSchedulerRoles sets who may read and execute on the scheduler and
// reads it back; an unknown role is refused and changes nothing.
func TestSchedulerRoles(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	var roles struct {
		CanRead    []string `json:"can_read"`
		CanExecute []string `json:"can_execute"`
	}
	in.JSON(t, &roles, "scheduler", "set-roles", "--read", "auditor", "--execute", "operator")
	require.Equal(t, []string{"auditor"}, roles.CanRead)
	require.Equal(t, []string{"operator"}, roles.CanExecute)
	in.JSON(t, &roles, "scheduler", "roles")
	require.Equal(t, []string{"auditor"}, roles.CanRead)

	t.Run("unknown role", func(t *testing.T) {
		finding(t, "PUT /scheduling/scheduler/roles accepts a role name the catalogue does not have ('emperor'); it should be a 400")
		in.Fails(t, harness.ExitError, "scheduler", "set-roles", "--read", "emperor")
		in.JSON(t, &roles, "scheduler", "roles")
		require.Equal(t, []string{"auditor"}, roles.CanRead, "a refused change changes nothing")
	})
}

// TestAuditTrail: what the scenarios did is in the audit log, the export is
// signed JSONL, and the attestations carry a trailer of their own.
func TestAuditTrail(t *testing.T) {
	requireInstance(t)
	org := createOrg(t, pcpctl, harness.Unique("qa-audited"))

	var entries page[struct {
		Event string `json:"event"`
		Actor struct {
			UserID string `json:"user_id"`
		} `json:"actor"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}]
	// An organization's creation is the first entry of its own log, not of
	// its parent's.
	pcpctl.In(org.ID).JSON(t, &entries, "audit", "list")
	var first struct {
		Event string `json:"event"`
		Actor struct {
			UserID string `json:"user_id"`
		} `json:"actor"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	for _, e := range entries.Items {
		if e.Event == "organization.created" {
			first = e
		}
	}
	require.Equal(t, "organization.created", first.Event, "the creation of %s is in its log", org.Name)
	require.Equal(t, org.ID, first.Organization.ID)
	t.Run("actor", func(t *testing.T) {
		finding(t, "the organization.created entry in the new organization's log names no actor")
		require.Equal(t, inst.UserID.String(), first.Actor.UserID, "logged against us")
	})

	t.Run("filters", func(t *testing.T) {
		finding(t, "GET /organizations/{id}/audit-logs ignores the event= and actor= filters pcpctl sends: every entry comes back whatever the filter")
		pcpctl.JSON(t, &entries, "audit", "list", "--event", "organization.created")
		for _, e := range entries.Items {
			require.Equal(t, "organization.created", e.Event)
		}
	})

	// The default export continues from the organization's export head, which
	// a new organization has never advanced, so its export holds its whole log.
	export := pcpctl.In(org.ID).OK(t, "audit", "export")
	require.Contains(t, export, `"event":"organization.created"`)
	require.Contains(t, export, `"seq":`)
	require.Contains(t, export, `"trailer":true`, "the export is sealed")

	var key struct {
		Algorithm string `json:"algorithm"`
		PublicKey string `json:"public_key"`
	}
	pcpctl.JSON(t, &key, "audit", "signing-key")
	require.Equal(t, "ed25519", key.Algorithm)
	require.True(t, strings.HasPrefix(key.PublicKey, "ssh-ed25519 "))

	t.Run("attestations", func(t *testing.T) {
		for kind, attests := range map[string]string{"residency": "residency", "legal-hold": "legal_hold"} {
			out := pcpctl.OK(t, "attestation", kind)
			require.Contains(t, out, `"trailer":true`)
			require.Contains(t, out, `"attests":"`+attests+`"`)
			pcpctl.JSON(t, &key, "attestation", "signing-key", kind)
			require.Equal(t, "ed25519", key.Algorithm)
		}
		pcpctl.Fails(t, harness.ExitError, "attestation", "residency", "--format", "csv")
		pcpctl.Fails(t, harness.ExitError, "attestation", "signing-key", "frobnication")
	})
	t.Run("bad filters", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "audit", "list", "--since", "yesterday")
		pcpctl.Fails(t, harness.ExitError, "audit", "export", "--format", "xml")
	})
}

// TestDeployment: the reads of the deployment itself answer, and the writes
// that need confirmation or a licence refuse without one.
func TestDeployment(t *testing.T) {
	requireInstance(t)

	var version struct {
		HasUpdate      bool   `json:"has_update"`
		CurrentVersion string `json:"current_version"`
	}
	pcpctl.JSON(t, &version, "deployment", "version")
	require.NotEmpty(t, version.CurrentVersion)
	require.False(t, version.HasUpdate, "the mock orkestrator offers no update")

	var packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	pcpctl.JSON(t, &packages, "deployment", "packages", "list")
	var s3 string
	for _, p := range packages {
		if p.Name == "s3" {
			s3 = p.Version
		}
	}
	require.NotEmpty(t, s3, "the rig installs s3")

	var safeMode struct {
		Enabled bool `json:"enabled"`
	}
	pcpctl.JSON(t, &safeMode, "deployment", "safe-mode")
	require.False(t, safeMode.Enabled)
	require.Contains(t, pcpctl.OK(t, "deployment", "debug-log"), "false")

	var interfaces struct {
		Interfaces []any `json:"interfaces"`
	}
	pcpctl.JSON(t, &interfaces, "deployment", "interfaces")
	require.NotEmpty(t, interfaces.Interfaces)

	var journal map[string]any
	pcpctl.JSON(t, &journal, "deployment", "journal")
	require.Contains(t, journal, "backups_total")
	var daily struct {
		Summaries []any `json:"summaries"`
	}
	pcpctl.JSON(t, &daily, "deployment", "journal", "--daily")
	require.NotEmpty(t, daily.Summaries)

	t.Run("refusals", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "deployment", "update")
		pcpctl.Fails(t, harness.ExitError, "deployment", "license", "not-a-licence")
		pcpctl.Fails(t, harness.ExitError, "deployment", "safe-mode", "--enable")
		pcpctl.Fails(t, harness.ExitError, "deployment", "packages", "delete", "s3", s3)
		pcpctl.JSON(t, &packages, "deployment", "packages", "list")
		require.Contains(t, pcpctl.OK(t, "deployment", "packages", "list"), "s3", "a refused removal removes nothing")
	})
	t.Run("remove a package never installed", func(t *testing.T) {
		finding(t, "DELETE /packages/{name}/{version} of a package never installed answers 200; every other id-taking route 404s")
		pcpctl.Fails(t, harness.ExitNotFound, "deployment", "packages", "delete", "nope", "v0.0.0", "--yes")
	})
}

// TestEdges: no edge is enrolled, and an enrollment key can be minted for one.
func TestEdges(t *testing.T) {
	requireInstance(t)

	var edges page[any]
	pcpctl.JSON(t, &edges, "edge", "list")
	require.Empty(t, edges.Items)

	var key struct {
		Edge struct {
			EnrollmentKey string `json:"enrollment_key"`
		} `json:"edge"`
	}
	pcpctl.JSON(t, &key, "edge", "enrollment-key")
	require.NotEmpty(t, key.Edge.EnrollmentKey)
}

// TestUsage: a wrong command line is a usage error, before anything reaches
// the server.
func TestUsage(t *testing.T) {
	requireInstance(t)

	pcpctl.Fails(t, harness.ExitUsage, "organization", "frobnicate")
	pcpctl.Fails(t, harness.ExitUsage, "frobnicate")
	pcpctl.Fails(t, harness.ExitUsage, "organization", "get")
	pcpctl.Fails(t, harness.ExitUsage, "organization", "list", "--no-such-flag")
	pcpctl.Fails(t, harness.ExitUsage, "organization", "list", "-o", "yaml")
}
