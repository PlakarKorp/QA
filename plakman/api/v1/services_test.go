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
	var s3ID, s3Version string
	for _, i := range installed.Items {
		if i.Name == "s3" {
			s3ID, s3Version = i.ID, i.Version
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
	require.Len(t, s3.Installed, 1)
	require.Equal(t, s3ID, s3.Installed[0].ID)

	var all page[struct {
		Name string `json:"name"`
	}]
	pcpctl.JSON(t, &all, "integration", "list")
	if !pcpctl.NotOnV2(t, "integration", "catalog", "s3") {
		// v2 has no catalogue of what could be installed.
		require.NotEmpty(t, s3.Available)
		var catalog page[struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}]
		pcpctl.JSON(t, &catalog, "integration", "catalog", "s3")
		require.Len(t, catalog.Items, 1)
		require.Equal(t, s3.Available[0].Version, catalog.Items[0].Version)
		require.Greater(t, all.Total, 1, "the catalogue offers more than what is installed")
	}

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
		if pcpctl.NotOnV2(t, "integration", "install", "s3", s3Version) {
			return
		}
		pcpctl.OK(t, "integration", "install", "s3", s3Version)
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

type bundle struct {
	ID              string   `json:"id"`
	InventoryID     string   `json:"inventory_id"`
	ResourceClasses []string `json:"resource_classes"`
	Tags            []string `json:"tags"`
}

func bundleIDs(t *testing.T, args ...string) []string {
	t.Helper()
	var bundles page[bundle]
	pcpctl.JSON(t, &bundles, append([]string{"bundle", "list"}, args...)...)
	ids := make([]string, len(bundles.Items))
	for i, b := range bundles.Items {
		ids[i] = b.ID
	}
	return ids
}

// TestBundles: a bundle is created with its match filter and nothing to
// configure -- the fields go through the form -- read back, found in the
// listing, replaced, and deleted, across the organization and confined to
// one inventory; the refusals are not-founds.
func TestBundles(t *testing.T) {
	requireInstance(t)

	var bundles page[bundle]
	pcpctl.JSON(t, &bundles, "bundle", "list")
	require.Equal(t, len(bundles.Items), bundles.Total)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	pcpctl.JSON(t, &bundles, "bundle", "list", "--inventory", inv.ID)
	require.Empty(t, bundles.Items, "a new inventory confines no bundle")

	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "delete", missing, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "list", "--inventory", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "create", "--integration", "nope", "--class", "object-storage")
	pcpctl.Fails(t, harness.ExitNotFound, "bundle", "update", missing, "--integration", "s3", "--class", "compute")

	t.Run("organization", func(t *testing.T) {
		var b bundle
		pcpctl.JSON(t, &b, "bundle", "create", "--integration", "s3", "--class", "object-storage", "--tag", "qa")
		require.NotEmpty(t, b.ID)
		require.Equal(t, []string{"object-storage"}, b.ResourceClasses)
		t.Cleanup(func() { pcpctl.Run(t, "bundle", "delete", b.ID, "--yes") })
		require.Contains(t, bundleIDs(t), b.ID)

		var got bundle
		pcpctl.JSON(t, &got, "bundle", "get", b.ID)
		require.Equal(t, b.ID, got.ID)
		require.Equal(t, []string{"qa"}, got.Tags)

		pcpctl.JSON(t, &got, "bundle", "update", b.ID, "--integration", "s3", "--class", "compute")
		require.Equal(t, []string{"compute"}, got.ResourceClasses)
		require.Empty(t, got.Tags, "an update replaces the bundle whole")

		pcpctl.Fails(t, harness.ExitError, "bundle", "delete", b.ID)
		pcpctl.OK(t, "bundle", "delete", b.ID, "--yes")
		require.NotContains(t, bundleIDs(t), b.ID)
		pcpctl.Fails(t, harness.ExitNotFound, "bundle", "get", b.ID)
	})
	t.Run("inventory", func(t *testing.T) {
		var b bundle
		pcpctl.JSON(t, &b, "bundle", "create", "--inventory", inv.ID, "--integration", "s3", "--class", "object-storage")
		require.Equal(t, inv.ID, b.InventoryID, "a confined bundle names its inventory")
		t.Cleanup(func() { pcpctl.Run(t, "bundle", "delete", b.ID, "--inventory", inv.ID, "--yes") })
		require.Contains(t, bundleIDs(t, "--inventory", inv.Name), b.ID)
		require.NotContains(t, bundleIDs(t), b.ID, "it is not among the organization's")

		var got bundle
		pcpctl.JSON(t, &got, "bundle", "get", b.ID, "--inventory", inv.ID)
		require.Equal(t, b.ID, got.ID)
		pcpctl.Fails(t, harness.ExitNotFound, "bundle", "get", b.ID)
		pcpctl.JSON(t, &got, "bundle", "update", b.ID, "--inventory", inv.ID, "--integration", "s3", "--class", "compute")
		require.Equal(t, []string{"compute"}, got.ResourceClasses)

		pcpctl.OK(t, "bundle", "delete", b.ID, "--inventory", inv.ID, "--yes")
		pcpctl.Fails(t, harness.ExitNotFound, "bundle", "get", b.ID, "--inventory", inv.ID)
	})
	t.Run("form", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "bundle", "form", "create", "--integration", "s3", "--type", "store", "--protocol", "s3", "--configuration", `{"root":"/backups"}`) {
			return
		}
		var created struct {
			ID            string         `json:"id"`
			Configuration map[string]any `json:"configuration"`
		}
		pcpctl.JSON(t, &created, "bundle", "form", "create", "--integration", "s3", "--type", "store", "--protocol", "s3",
			"--class", "object-storage", "--configuration", `{"root":"/backups"}`)
		require.NotEmpty(t, created.ID)
		t.Cleanup(func() { pcpctl.Run(t, "bundle", "delete", created.ID, "--yes") })
		require.Contains(t, bundleIDs(t), created.ID, "a form bundle is a bundle")

		var form struct {
			ID            string         `json:"id"`
			Type          string         `json:"type"`
			Protocol      string         `json:"protocol"`
			Configuration map[string]any `json:"configuration"`
			Schema        map[string]any `json:"schema"`
		}
		pcpctl.JSON(t, &form, "bundle", "form", "get", created.ID)
		require.Equal(t, "store", form.Type)
		require.Equal(t, "s3", form.Protocol)
		require.Equal(t, "/backups", form.Configuration["root"])
		require.NotEmpty(t, form.Schema)

		pcpctl.OK(t, "bundle", "form", "update", created.ID, "--configuration", `{"root":"/elsewhere"}`)
		pcpctl.JSON(t, &form, "bundle", "form", "get", created.ID)
		require.Equal(t, "/elsewhere", form.Configuration["root"])

		var secrets map[string]any
		pcpctl.JSON(t, &secrets, "bundle", "secrets", created.ID)
		require.Empty(t, secrets, "a root is not a secret")

		// A bundle made with fields has no form to read.
		var legacy bundle
		pcpctl.JSON(t, &legacy, "bundle", "create", "--integration", "s3", "--class", "object-storage")
		t.Cleanup(func() { pcpctl.Run(t, "bundle", "delete", legacy.ID, "--yes") })
		pcpctl.Fails(t, harness.ExitConflict, "bundle", "form", "get", legacy.ID)

		var confined struct {
			ID string `json:"id"`
		}
		pcpctl.JSON(t, &confined, "bundle", "form", "create", "--inventory", inv.ID, "--integration", "s3", "--type", "store", "--protocol", "s3",
			"--configuration", `{"root":"/confined"}`)
		t.Cleanup(func() { pcpctl.Run(t, "bundle", "delete", confined.ID, "--inventory", inv.ID, "--yes") })
		pcpctl.JSON(t, &form, "bundle", "form", "get", confined.ID, "--inventory", inv.ID)
		require.Equal(t, "/confined", form.Configuration["root"])
		pcpctl.OK(t, "bundle", "form", "update", confined.ID, "--inventory", inv.ID, "--configuration", `{"root":"/moved"}`)
		pcpctl.JSON(t, &secrets, "bundle", "secrets", confined.ID, "--inventory", inv.ID)
		require.Empty(t, secrets)

		pcpctl.Fails(t, harness.ExitNotFound, "bundle", "form", "get", missing)
		pcpctl.Fails(t, harness.ExitNotFound, "bundle", "secrets", missing)
		pcpctl.Fails(t, harness.ExitUsage, "bundle", "form", "create", "--integration", "s3", "--configuration", "{}")
	})
}

// TestSLA: nothing is contracted on a fresh instance, the reads answer their
// empty pages, and a contract or template that does not exist is a
// not-found. pcpctl offers no template create, so the contract path is
// covered by its refusals.
func TestSLA(t *testing.T) {
	requireInstance(t)

	if !pcpctl.NotOnV2(t, "sla", "list") {
		var slas page[map[string]any]
		pcpctl.JSON(t, &slas, "sla", "list")
		require.Equal(t, len(slas.Items), slas.Total)
	}
	var templates []map[string]any
	pcpctl.JSON(t, &templates, "sla", "templates", "list")
	var contracts []map[string]any
	pcpctl.JSON(t, &contracts, "sla", "contracts", "list")
	var resources page[map[string]any]
	pcpctl.JSON(t, &resources, "sla", "resources")

	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "delete", missing, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "update", missing, "--name", "ghost")
	if harness.OnV2() {
		// v2 reports a template that is not there as a bad argument.
		pcpctl.Fails(t, harness.ExitError, "sla", "contracts", "create", missing, missing)
	} else {
		pcpctl.Fails(t, harness.ExitNotFound, "sla", "contracts", "create", missing, missing)
	}
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
	if !in.NotOnV2(t, "scheduler", "nearby") {
		in.JSON(t, &nearby, "scheduler", "nearby")
		require.Empty(t, nearby)
	}

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
	if !pcpctl.NotOnV2(t, "deployment", "debug-log") {
		require.Contains(t, pcpctl.OK(t, "deployment", "debug-log"), "false")
	}

	if !pcpctl.NotOnV2(t, "deployment", "interfaces") {
		var interfaces struct {
			Interfaces []any `json:"interfaces"`
		}
		pcpctl.JSON(t, &interfaces, "deployment", "interfaces")
		require.NotEmpty(t, interfaces.Interfaces)
	}

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
		pcpctl.NotOnV2(t, "deployment", "license", "not-a-licence")
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

// TestSchedulerLifecycle: the caller makes a scheduler of their own beside
// the organization's default one, cannot make a second, sees it among the
// user schedulers, deletes it, and cannot delete what is gone. The badge
// keeps resolving to the default scheduler throughout.
func TestSchedulerLifecycle(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	var s struct {
		ID    string `json:"id"`
		Actor string `json:"actor"`
		Name  string `json:"name"`
	}
	in.JSON(t, &s, "scheduler", "create")
	require.NotEmpty(t, s.ID)
	require.Equal(t, "user", s.Actor)
	t.Cleanup(func() { in.Run(t, "scheduler", "delete", "--yes") })

	if harness.OnV2() {
		// v2 answers 500 to a second scheduler: api/README.md.
		in.Fails(t, harness.ExitError, "scheduler", "create")
	} else {
		in.Fails(t, harness.ExitConflict, "scheduler", "create")
	}

	var users page[struct {
		ID    string `json:"id"`
		Actor string `json:"actor"`
	}]
	in.JSON(t, &users, "scheduler", "users")
	require.Len(t, users.Items, 1)
	require.Equal(t, s.ID, users.Items[0].ID)

	var current struct {
		Actor string `json:"actor"`
	}
	in.JSON(t, &current, "scheduler", "get")
	require.Equal(t, "default", current.Actor, "the badge resolves to the default scheduler, not the caller's own")

	in.Fails(t, harness.ExitError, "scheduler", "delete")
	in.OK(t, "scheduler", "delete", "--yes")
	in.JSON(t, &users, "scheduler", "users")
	require.Empty(t, users.Items)
	in.Fails(t, harness.ExitNotFound, "scheduler", "delete", "--yes")
}

type task struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Origin *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"origin"`
	Target *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"target"`
}

func taskIDs(t *testing.T, p *harness.Pcpctl) []string {
	t.Helper()
	var tasks page[task]
	p.JSON(t, &tasks, "task", "list", "--limit", "200")
	ids := make([]string, len(tasks.Items))
	for i, x := range tasks.Items {
		ids[i] = x.ID
	}
	return ids
}

// TestTaskLifecycle adds a check task on a store, finds it in the listing,
// replaces its definition, runs it once, deletes it, and sees it gone; a task
// with nothing to run on, or of no type, is refused before it is made.
func TestTaskLifecycle(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false")

	var created task
	pcpctl.JSON(t, &created, "task", "create", "--type", "check", "--origin", c.ID)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "check", created.Type)
	require.NotNil(t, created.Origin)
	require.Equal(t, c.ID, created.Origin.ID)
	require.Equal(t, c.Name, created.Origin.Name, "the task names its connector")
	t.Cleanup(func() { pcpctl.Run(t, "task", "delete", created.ID, "--yes") })
	require.Contains(t, taskIDs(t, pcpctl), created.ID)

	var edited task
	pcpctl.JSON(t, &edited, "task", "edit", created.ID, "--definition",
		`{"type":"check","origin":{"id":"`+c.ID+`"},"schedule":{"enabled":false,"rules":[]},"config":{}}`)
	require.Equal(t, created.ID, edited.ID, "an edit keeps the task's id")
	require.Equal(t, "check", edited.Type)

	pcpctl.OK(t, "task", "run", created.ID)

	t.Run("by name", func(t *testing.T) {
		var byName task
		pcpctl.JSON(t, &byName, "task", "create", "--type", "check", "--origin", c.Name)
		require.Equal(t, c.ID, byName.Origin.ID, "--origin resolves a name")
		pcpctl.OK(t, "task", "delete", byName.ID, "--yes")
	})
	t.Run("refusals", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "task", "create", "--origin", c.ID)
		if harness.OnV2() {
			// Unlike v1, v2 takes a task with nothing to run on.
			var bare task
			pcpctl.JSON(t, &bare, "task", "create", "--type", "check")
			pcpctl.OK(t, "task", "delete", bare.ID, "--yes")
		} else {
			pcpctl.Fails(t, harness.ExitError, "task", "create", "--type", "check")
		}
		// A connector that names nothing is a bad parameter to the server,
		// not a not-found, as it is for a connector's resource.
		res := pcpctl.Fails(t, harness.ExitError, "task", "create", "--type", "check", "--origin", missing)
		if !harness.OnV2() {
			// v2 says invalid_argument without naming the field.
			require.Contains(t, res.Stderr, "Origin")
		}
		pcpctl.Fails(t, harness.ExitError, "task", "create", "--type", "check", "--origin", c.ID, "--schedule", "{nope")
		pcpctl.Fails(t, harness.ExitNotFound, "task", "edit", missing, "--definition",
			`{"type":"check","origin":{"id":"`+c.ID+`"},"schedule":{"enabled":false,"rules":[]},"config":{}}`)
		pcpctl.Fails(t, harness.ExitError, "task", "edit", created.ID)
	})

	pcpctl.Fails(t, harness.ExitError, "task", "delete", created.ID)
	pcpctl.OK(t, "task", "delete", created.ID, "--yes")
	require.NotContains(t, taskIDs(t, pcpctl), created.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "task", "delete", created.ID, "--yes")
}

// TestOneOffRun queues a task to run once, outside any schedule, reads the
// run back by its id, and refuses a run with nothing to run on.
func TestOneOffRun(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false")

	var run struct {
		ID string `json:"id"`
	}
	pcpctl.JSON(t, &run, "scheduler", "run", "--type", "check", "--origin", c.ID)
	require.NotEmpty(t, run.ID)

	var got struct {
		ID string `json:"id"`
	}
	pcpctl.JSON(t, &got, "scheduler", "at", run.ID)
	require.Equal(t, run.ID, got.ID)

	pcpctl.Fails(t, harness.ExitNotFound, "scheduler", "at", missing)
	pcpctl.Fails(t, harness.ExitUsage, "scheduler", "run", "--origin", c.ID)
	if !harness.OnV2() {
		// v2 runs a task with nothing to run on, as it makes one.
		pcpctl.Fails(t, harness.ExitError, "scheduler", "run", "--type", "check")
	}
	res := pcpctl.Fails(t, harness.ExitError, "scheduler", "run", "--type", "check", "--origin", missing)
	if !harness.OnV2() {
		// v2 says invalid_argument without naming the field.
		require.Contains(t, res.Stderr, "Origin", "a connector that names nothing is a bad parameter, not a not-found")
	}
	pcpctl.Fails(t, harness.ExitError, "scheduler", "run", "--type", "check", "--origin", c.ID, "--at", "tomorrow")
}

type classAccess struct {
	CanRead    []string `json:"can_read"`
	CanExecute []string `json:"can_execute"`
	CanWrite   []string `json:"can_write"`
	CanOwn     []string `json:"can_own"`
}

// TestSchedulerAccess sets who holds which role on the scheduler, as
// role-to-group assignments, and reads it back as verbs; widening is refused
// until confirmed, and an empty set clears it.
func TestSchedulerAccess(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)
	var g group
	in.JSON(t, &g, "group", "create", harness.Unique("qa-readers"))
	t.Cleanup(func() { in.Run(t, "group", "delete", g.ID, "--yes") })

	var access classAccess
	in.JSON(t, &access, "scheduler", "access", "get")
	require.Empty(t, access.CanRead)

	if !harness.OnV2() {
		// v1 guards against a change that gives anyone more than they had;
		// v2 has no such guard.
		in.Fails(t, harness.ExitConflict, "scheduler", "access", "set", "--assign", "operator="+g.Name)
		in.JSON(t, &access, "scheduler", "access", "get")
		require.Empty(t, access.CanRead, "a refused change changes nothing")
	}

	in.JSON(t, &access, "scheduler", "access", "set", "--assign", "operator="+g.Name, "--confirm-widen")
	require.NotEmpty(t, access.CanRead, "an operator reads")
	require.NotEmpty(t, access.CanExecute, "an operator executes")
	in.JSON(t, &access, "scheduler", "access", "get")
	require.NotEmpty(t, access.CanRead)

	in.JSON(t, &access, "scheduler", "access", "set")
	require.Empty(t, access.CanRead, "an empty set clears the rule")

	in.Fails(t, harness.ExitNotFound, "scheduler", "access", "set", "--assign", "operator="+harness.Unique("no-such-group"))
	in.Fails(t, harness.ExitError, "scheduler", "access", "set", "--assign", "operator")
}

// TestClassAccess does the same for a class of resources: inventories here.
func TestClassAccess(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)
	var g group
	in.JSON(t, &g, "group", "create", harness.Unique("qa-readers"))
	t.Cleanup(func() { in.Run(t, "group", "delete", g.ID, "--yes") })

	var access classAccess
	in.JSON(t, &access, "grant", "class", "get", "inventory")
	require.Empty(t, access.CanRead)

	if !harness.OnV2() {
		in.Fails(t, harness.ExitConflict, "grant", "class", "set", "inventory", "--assign", "operator="+g.ID)
	}
	in.JSON(t, &access, "grant", "class", "set", "inventory", "--assign", "operator="+g.ID, "--confirm-widen")
	require.NotEmpty(t, access.CanRead)
	in.JSON(t, &access, "grant", "class", "get", "inventory")
	require.NotEmpty(t, access.CanRead)
	in.JSON(t, &access, "grant", "class", "set", "inventory")
	require.Empty(t, access.CanRead)

	in.Fails(t, harness.ExitError, "grant", "class", "get", "frobnicate")
}

// TestSchedulerJournal exports the scheduler's job journal as CSV: a header
// naming the columns, whatever --output says.
func TestSchedulerJournal(t *testing.T) {
	requireInstance(t)
	if pcpctl.NotOnV2(t, "scheduler", "journal") {
		return
	}

	out := pcpctl.OK(t, "scheduler", "journal")
	require.True(t, strings.HasPrefix(out, "job_id,"), "the journal is CSV with a header: %q", out)
	require.Equal(t, out, pcpctl.OK(t, "scheduler", "journal", "-o", "json"), "the CSV goes out as it came whatever --output says")
	pcpctl.Fails(t, harness.ExitError, "scheduler", "journal", "--since", "yesterday")
}

// TestSnapshotsOfSource: a source holds no snapshots on a fresh store, the
// reads of what is not there are not-founds, and a prune of an empty store
// previews and runs to nothing.
func TestSnapshotsOfSource(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false")
	var src connector
	pcpctl.JSON(t, &src, "connector", "create", harness.Unique("qa-source"), "--type", "source", "--protocol", "s3",
		"--integration", "s3", "--resource", r.URNID,
		"--field", "access_key=qa", "--field", "secret_access_key=qa", "--field", "root=/bucket")
	t.Cleanup(func() { pcpctl.Run(t, "connector", "delete", src.ID, "--yes") })

	var snaps page[map[string]any]
	pcpctl.JSON(t, &snaps, "snapshot", "list", "--source", src.ID)
	require.Empty(t, snaps.Items)
	pcpctl.JSON(t, &snaps, "snapshot", "list", "--source", src.Name)
	require.Empty(t, snaps.Items, "--source takes a name")

	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "get", "deadbeef", "--source", src.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "get", "deadbeef", "--store", c.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "hold", "deadbeef", "--store", c.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "hold", "deadbeef")
	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "hold", "deadbeef", "--release")
	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "stores", "deadbeef")
	pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "list", "--source", missing)

	t.Run("prune", func(t *testing.T) {
		var preview struct {
			WouldDelete []string `json:"would_delete"`
			WouldKeep   []string `json:"would_keep"`
		}
		pcpctl.JSON(t, &preview, "snapshot", "prune", "--store", c.ID, "--config", `{"per_day":1}`)
		require.Empty(t, preview.WouldDelete)
		require.Empty(t, preview.WouldKeep)

		pcpctl.Fails(t, harness.ExitError, "snapshot", "prune", "--store", c.ID, "--config", `{"per_day":1}`, "--run")
		var run map[string]any
		pcpctl.JSON(t, &run, "snapshot", "prune", "--store", c.ID, "--config", `{"per_day":1}`, "--run", "--yes")
		require.Contains(t, run, "deleted")
		require.Empty(t, run["deleted"])

		pcpctl.Fails(t, harness.ExitError, "snapshot", "prune", "--store", c.ID)
		pcpctl.Fails(t, harness.ExitError, "snapshot", "prune", "--config", `{"per_day":1}`)
		pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "prune", "--store", missing, "--config", `{"per_day":1}`)
	})
}

// TestConnectorExtras: what the caller may do to a connector, the secrets it
// holds, its configuration as form data, and the store behind it.
func TestConnectorExtras(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false", "--field", "passphrase=qa")

	t.Run("capabilities", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "connector", "capabilities", c.ID) {
			return
		}
		var caps struct {
			Resource string   `json:"resource"`
			ID       string   `json:"id"`
			Actions  []string `json:"actions"`
		}
		pcpctl.JSON(t, &caps, "connector", "capabilities", c.ID)
		require.Equal(t, "connector", caps.Resource)
		require.Equal(t, c.ID, caps.ID)
		require.Contains(t, caps.Actions, "view")
		require.Contains(t, caps.Actions, "delete")
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "capabilities", missing)
	})
	t.Run("secrets", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "connector", "secrets", c.ID) {
			return
		}
		var secrets map[string]any
		pcpctl.JSON(t, &secrets, "connector", "secrets", c.ID)
		require.Equal(t, "qa", secrets["secret_access_key"], "the reveal answers the literal value")
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "secrets", missing)
	})
	t.Run("form", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "connector", "form", "get", c.ID) {
			return
		}
		var form struct {
			ID            string         `json:"id"`
			Configuration map[string]any `json:"configuration"`
			Schema        map[string]any `json:"schema"`
		}
		pcpctl.JSON(t, &form, "connector", "form", "get", c.ID)
		require.Equal(t, c.ID, form.ID)
		require.Equal(t, "/bucket", form.Configuration["root"])
		require.NotEmpty(t, form.Schema, "the form comes with its schema")
		_, sensitive := form.Configuration["secret_access_key"].(map[string]any)
		require.True(t, sensitive, "a sensitive value is filtered, not shown")

		pcpctl.OK(t, "connector", "form", "update", c.ID, "--configuration",
			`{"access_key":"qa","secret_access_key":"qa","passphrase":"qa","root":"/elsewhere"}`)
		pcpctl.JSON(t, &form, "connector", "form", "get", c.ID)
		require.Equal(t, "/elsewhere", form.Configuration["root"])

		name := harness.Unique("qa-form-store")
		var created struct {
			ID string `json:"id"`
		}
		pcpctl.JSON(t, &created, "connector", "form", "create", name, "--type", "store", "--protocol", "s3",
			"--integration", "s3", "--resource", r.URNID,
			"--configuration", `{"access_key":"qa","secret_access_key":"qa","passphrase":"qa","root":"/formed"}`)
		require.NotEmpty(t, created.ID)
		t.Cleanup(func() { pcpctl.Run(t, "connector", "delete", created.ID, "--yes") })
		var got connector
		pcpctl.JSON(t, &got, "connector", "get", name)
		require.Equal(t, created.ID, got.ID, "a connector made from a form is a connector")

		// The schema is what decides: a configuration it does not accept is
		// refused, as is one that is not JSON.
		pcpctl.Fails(t, harness.ExitError, "connector", "form", "create", harness.Unique("qa-bad"), "--type", "store", "--protocol", "s3",
			"--integration", "s3", "--resource", r.URNID, "--configuration", `{"root":"/x"}`)
		pcpctl.Fails(t, harness.ExitError, "connector", "form", "update", c.ID, "--configuration", "{nope")
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "form", "get", missing)
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "form", "update", missing, "--configuration", "{}")
		pcpctl.Fails(t, harness.ExitUsage, "connector", "form", "update", c.ID)
	})
	t.Run("create-store", func(t *testing.T) {
		// The store behind the connector is made where the connector points,
		// which is a loopback port nobody listens on. v1 answers the request
		// before the executor finds out; v2 waits for the verdict.
		res := pcpctl.Run(t, "connector", "create-store", c.ID)
		if harness.OnV2() {
			require.Equal(t, harness.ExitError, res.Code, "%s", res)
		} else {
			require.Equal(t, harness.ExitOK, res.Code, "%s", res)
		}
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "create-store", missing)
	})
}

// TestBackupAndRestoreViews: the backup view lists a resource with the sources
// that protect it, the restore view lists only what holds a snapshot, and a
// source with no backup has no restore points.
func TestBackupAndRestoreViews(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false")
	var src connector
	pcpctl.JSON(t, &src, "connector", "create", harness.Unique("qa-source"), "--type", "source", "--protocol", "s3",
		"--integration", "s3", "--resource", r.URNID,
		"--field", "access_key=qa", "--field", "secret_access_key=qa", "--field", "root=/bucket")
	t.Cleanup(func() { pcpctl.Run(t, "connector", "delete", src.ID, "--yes") })

	if pcpctl.NotOnV2(t, "backup", "list") {
		pcpctl.NotOnV2(t, "restore", "list")
		pcpctl.NotOnV2(t, "restore", "points", src.ID)
		return
	}

	type viewed struct {
		URNID   string `json:"urn_id"`
		Name    string `json:"name"`
		Sources []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"sources"`
	}
	var backups page[viewed]
	pcpctl.JSON(t, &backups, "backup", "list", "--inventory", r.InventoryID)
	require.Len(t, backups.Items, 1)
	require.Equal(t, r.URNID, backups.Items[0].URNID)
	require.Len(t, backups.Items[0].Sources, 1)
	require.Equal(t, src.ID, backups.Items[0].Sources[0].ID, "the view names the source that protects the resource")

	var one viewed
	pcpctl.JSON(t, &one, "backup", "get", r.URNID)
	require.Equal(t, r.URNID, one.URNID)
	pcpctl.JSON(t, &one, "backup", "get", r.Name)
	require.Equal(t, r.URNID, one.URNID, "get resolves a name")
	pcpctl.Fails(t, harness.ExitNotFound, "backup", "get", missing)

	pcpctl.JSON(t, &backups, "backup", "list", "--inventory", r.InventoryID, "--policy", "not_covered")
	require.Len(t, backups.Items, 1, "a source no policy schedules is not covered")
	pcpctl.JSON(t, &backups, "backup", "list", "--inventory", r.InventoryID, "--policy", "covered")
	require.Empty(t, backups.Items)
	pcpctl.Fails(t, harness.ExitError, "backup", "list", "--policy", "sometimes")

	var restores page[viewed]
	pcpctl.JSON(t, &restores, "restore", "list", "--inventory", r.InventoryID)
	require.Empty(t, restores.Items, "nothing holds a snapshot yet")
	pcpctl.Fails(t, harness.ExitNotFound, "restore", "get", r.URNID)
	pcpctl.Fails(t, harness.ExitNotFound, "restore", "get", missing)

	var points page[map[string]any]
	pcpctl.JSON(t, &points, "restore", "points", src.ID)
	require.Empty(t, points.Items)
	pcpctl.JSON(t, &points, "restore", "points", src.Name, "--store", c.Name)
	require.Empty(t, points.Items, "the source and the store resolve by name")
	pcpctl.Fails(t, harness.ExitError, "restore", "points", c.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "restore", "points", missing)
	pcpctl.Fails(t, harness.ExitError, "restore", "points", src.ID, "--since", "yesterday")
}

// TestIntegrationDetail reads one integration every way the API offers: by
// name and version, by id, as a form schema, as the current version for a
// connector type, and as an installed version with what it supports.
func TestIntegrationDetail(t *testing.T) {
	requireInstance(t)

	var installed page[struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}]
	pcpctl.JSON(t, &installed, "integration", "installed")
	var s3ID, s3Version string
	for _, i := range installed.Items {
		if i.Name == "s3" {
			s3ID, s3Version = i.ID, i.Version
		}
	}
	require.NotEmpty(t, s3ID, "the rig installs s3")

	var ref struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Version   string `json:"version"`
		Protocols []struct {
			Scheme string `json:"scheme"`
		} `json:"protocols"`
	}
	pcpctl.JSON(t, &ref, "integration", "version", "s3", s3Version)
	require.Equal(t, s3ID, ref.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "integration", "version", "s3", "v0.0.0")
	pcpctl.Fails(t, harness.ExitNotFound, "integration", "version", "nope", s3Version)

	pcpctl.JSON(t, &ref, "integration", "describe", s3ID, "--type", "store")
	require.Equal(t, "s3", ref.Name)
	require.Equal(t, s3Version, ref.Version)

	var current page[struct {
		ID string `json:"id"`
	}]
	pcpctl.JSON(t, &current, "integration", "current", "--type", "store")
	var found bool
	for _, i := range current.Items {
		found = found || i.ID == s3ID
	}
	require.True(t, found, "s3 is current for a store")
	pcpctl.JSON(t, &current, "integration", "current", "--type", "store", "--name", "nope")
	require.Empty(t, current.Items)

	var one struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		IsCurrent bool     `json:"is_current"`
		Supports  []string `json:"supports"`
	}
	pcpctl.JSON(t, &one, "integration", "installed", "get", s3ID)
	require.Equal(t, "s3", one.Name)
	require.True(t, one.IsCurrent)
	pcpctl.JSON(t, &one, "integration", "installed", "summary", s3ID)
	require.NotEmpty(t, one.Supports, "the summary says what the version supports")
	pcpctl.Fails(t, harness.ExitNotFound, "integration", "installed", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "integration", "installed", "summary", missing)

	t.Run("protocols", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "integration", "schema", s3ID, "--type", "store") {
			return
		}
		pcpctl.JSON(t, &ref, "integration", "version", "s3", s3Version, "--type", "store")
		require.NotEmpty(t, ref.Protocols, "with a type, the protocols come too")
		require.Equal(t, "s3", ref.Protocols[0].Scheme)

		var schema struct {
			Protocols []struct {
				Scheme string         `json:"scheme"`
				Schema map[string]any `json:"schema"`
			} `json:"protocols"`
		}
		pcpctl.JSON(t, &schema, "integration", "schema", s3ID, "--type", "store")
		require.NotEmpty(t, schema.Protocols)
		require.Contains(t, schema.Protocols[0].Schema, "properties")
		pcpctl.Fails(t, harness.ExitNotFound, "integration", "schema", missing, "--type", "store")
	})
	t.Run("install and make current", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "integration", "install", "nope", "v1.0.0", "--current") {
			return
		}
		finding(t, "POST /integrations/installed/current with a name the catalogue does not offer answers 500, as POST /integrations/installed does")
		pcpctl.Fails(t, harness.ExitNotFound, "integration", "install", "nope", "v1.0.0", "--current")
	})
}

// TestSecretProviderCreate: a vault is an instance of a secret-provider
// integration, and the rig installs none, so every creation is refused; the
// reads of a vault that does not exist are not-founds.
func TestSecretProviderCreate(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-vault")

	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "create", name, "--integration", "nope", "--protocol", "x")
	pcpctl.Fails(t, harness.ExitUsage, "secret-provider", "create", name, "--protocol", "x")
	pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "get", name)

	t.Run("not a secret provider", func(t *testing.T) {
		if harness.OnV2() {
			pcpctl.Fails(t, harness.ExitError, "secret-provider", "create", name, "--integration", "s3", "--protocol", "s3", "--field", "a=b")
			return
		}
		finding(t, "POST /secret_providers with an integration that is not a secret provider (s3) answers 500")
		pcpctl.Fails(t, harness.ExitError, "secret-provider", "create", name, "--integration", "s3", "--protocol", "s3", "--field", "a=b")
	})
	t.Run("absent", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "secret-provider", "capabilities", missing) {
			pcpctl.NotOnV2(t, "secret-provider", "secrets", missing)
			pcpctl.NotOnV2(t, "secret-provider", "form", "get", missing)
			return
		}
		pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "capabilities", missing)
		pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "secrets", missing)
		pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "form", "get", missing)
		pcpctl.Fails(t, harness.ExitNotFound, "secret-provider", "form", "update", missing, "--configuration", "{}")
		pcpctl.Fails(t, harness.ExitError, "secret-provider", "form", "create", name, "--integration", "s3", "--protocol", "s3", "--configuration", "{}")
	})
}

// TestSLATemplates creates a template with a temporality pinned to a store,
// changes it, binds it to a source, reads the contract back, unbinds it,
// deletes the template, and sees it gone.
func TestSLATemplates(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false")
	var src connector
	pcpctl.JSON(t, &src, "connector", "create", harness.Unique("qa-source"), "--type", "source", "--protocol", "s3",
		"--integration", "s3", "--resource", r.URNID,
		"--field", "access_key=qa", "--field", "secret_access_key=qa", "--field", "root=/bucket")
	t.Cleanup(func() { pcpctl.Run(t, "connector", "delete", src.ID, "--yes") })

	type template struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Status      string   `json:"status"`
		Environment string   `json:"environment"`
		Tags        []string `json:"tags"`
		Revision    int      `json:"revision"`
	}
	name := harness.Unique("qa-sla")
	var created template
	pcpctl.JSON(t, &created, "sla", "templates", "create", name, "--environment", "production",
		"--spec", `{"temporalities":{"day":{"frequency":1,"retention":7,"store_id":"`+c.ID+`"}}}`)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	require.Equal(t, "production", created.Environment)
	require.NotEmpty(t, created.Status)
	t.Cleanup(func() { pcpctl.Run(t, "sla", "templates", "delete", created.ID, "--yes") })

	var got template
	pcpctl.JSON(t, &got, "sla", "templates", "get", created.ID)
	require.Equal(t, created.ID, got.ID)
	var templates []template
	pcpctl.JSON(t, &templates, "sla", "templates", "list")
	var found bool
	for _, x := range templates {
		found = found || x.ID == created.ID
	}
	require.True(t, found)

	pcpctl.JSON(t, &got, "sla", "templates", "update", created.ID, "--tag", "qa")
	require.Equal(t, []string{"qa"}, got.Tags)
	require.Greater(t, got.Revision, created.Revision, "a change is a new revision")

	type contract struct {
		ID         string `json:"id"`
		TemplateID string `json:"template_id"`
		SourceID   string `json:"source_id"`
	}
	var ct contract
	pcpctl.JSON(t, &ct, "sla", "contracts", "create", created.ID, src.ID)
	require.Equal(t, created.ID, ct.TemplateID)
	require.Equal(t, src.ID, ct.SourceID)
	var read contract
	pcpctl.JSON(t, &read, "sla", "contracts", "get", ct.ID)
	require.Equal(t, ct, read)
	var contracts []contract
	pcpctl.JSON(t, &contracts, "sla", "contracts", "list")
	found = false
	for _, x := range contracts {
		found = found || x.ID == ct.ID
	}
	require.True(t, found)

	t.Run("service levels", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "sla", "list") {
			return
		}
		var slas page[struct {
			ID string `json:"id"`
		}]
		pcpctl.JSON(t, &slas, "sla", "list")
		var found bool
		for _, s := range slas.Items {
			found = found || s.ID == created.ID
		}
		require.True(t, found, "a template with a contract is a service level")
	})

	pcpctl.OK(t, "sla", "contracts", "delete", ct.ID, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "contracts", "get", ct.ID)
	pcpctl.OK(t, "sla", "templates", "delete", created.ID, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "sla", "templates", "get", created.ID)

	t.Run("refusals", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "sla", "templates", "create")
		pcpctl.Fails(t, harness.ExitError, "sla", "templates", "create", harness.Unique("qa-sla"), "--spec", "{nope")
		t.Run("contract on an absent template", func(t *testing.T) {
			finding(t, "POST /slas/contracts with a template that does not exist and a source that does answers 500; with both absent it answers 404")
			pcpctl.Fails(t, harness.ExitNotFound, "sla", "contracts", "create", missing, src.ID)
		})
		if !harness.OnV2() {
			// A temporality with a frequency needs a store to keep it on.
			pcpctl.Fails(t, harness.ExitError, "sla", "templates", "create", harness.Unique("qa-sla"),
				"--spec", `{"temporalities":{"day":{"frequency":1,"retention":7}}}`)
		}
	})
}

// TestDeploymentExtras: the deployment describes itself, dumps its database,
// lists the webhook events, switches debug logging on for a window and off,
// and enters and leaves safe mode; what the rig's host cannot do -- the
// appliance specs, the network, the log files -- refuses as it does.
func TestDeploymentExtras(t *testing.T) {
	requireInstance(t)

	var d struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Enrolled bool   `json:"enrolled"`
	}
	pcpctl.JSON(t, &d, "deployment", "get")
	require.NotEmpty(t, d.Version)
	require.True(t, d.Enrolled)

	var events map[string]any
	pcpctl.JSON(t, &events, "deployment", "webhook-events")
	require.NotEmpty(t, events)

	t.Run("database dump", func(t *testing.T) {
		pcpctl.OK(t, "deployment", "database-dump")
		finding(t, "GET /database/dump answers 200 with an empty body in the rig: the daemon runs where no pg_dump is")
		require.Contains(t, pcpctl.OK(t, "deployment", "database-dump"), "CREATE TABLE", "the dump is the database's SQL")
	})

	t.Run("safe mode", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "deployment", "safe-mode", "--enable")
		pcpctl.OK(t, "deployment", "safe-mode", "--enable", "--yes")
		t.Cleanup(func() { pcpctl.Run(t, "deployment", "safe-mode", "--disable") })
		var state struct {
			Enabled bool `json:"enabled"`
		}
		pcpctl.JSON(t, &state, "deployment", "safe-mode")
		require.True(t, state.Enabled)
		pcpctl.OK(t, "deployment", "safe-mode", "--disable")
		pcpctl.JSON(t, &state, "deployment", "safe-mode")
		require.False(t, state.Enabled)
		pcpctl.Fails(t, harness.ExitError, "deployment", "safe-mode", "--enable", "--disable")
	})
	t.Run("debug log", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "deployment", "debug-log", "--enable") {
			return
		}
		pcpctl.OK(t, "deployment", "debug-log", "--enable", "--revert-after", "1m")
		t.Cleanup(func() { pcpctl.Run(t, "deployment", "debug-log", "--disable") })
		require.Contains(t, pcpctl.OK(t, "deployment", "debug-log"), "true")
		pcpctl.OK(t, "deployment", "debug-log", "--disable")
		require.Contains(t, pcpctl.OK(t, "deployment", "debug-log"), "false")
	})
	t.Run("update", func(t *testing.T) {
		// The mock orkestrator offers no update, so there is nothing to go to.
		pcpctl.Fails(t, harness.ExitError, "deployment", "update", "--yes")
		if harness.OnV2() {
			pcpctl.Fails(t, harness.ExitError, "deployment", "update", "--version", "v0.0.0", "--yes")
			return
		}
		finding(t, "POST /update-to-version with a version the feed does not offer answers 500 through the mock orkestrator")
		pcpctl.Fails(t, harness.ExitError, "deployment", "update", "--version", "v0.0.0", "--yes")
	})
	t.Run("logs", func(t *testing.T) {
		// The rig runs the daemon in the foreground, so it writes no log
		// file of its own: the day's log is a not-found on v1, and a 500 on
		// v2 (api/README.md).
		if harness.OnV2() {
			pcpctl.Fails(t, harness.ExitError, "deployment", "logs")
		} else {
			pcpctl.Fails(t, harness.ExitNotFound, "deployment", "logs")
		}
		pcpctl.Fails(t, harness.ExitError, "deployment", "logs", "--date", "yesterday")
	})
	t.Run("appliance", func(t *testing.T) {
		if pcpctl.NotOnV2(t, "deployment", "specs") {
			pcpctl.NotOnV2(t, "deployment", "interfaces", "configure", "lo0", "--mtu", "1500", "--yes")
			return
		}
		// The mock orkestrator serves the interfaces, and neither the specs
		// nor a reconfiguration.
		pcpctl.Fails(t, harness.ExitError, "deployment", "specs")
		pcpctl.Fails(t, harness.ExitError, "deployment", "interfaces", "configure", "lo0", "--mtu", "1500", "--yes")
		pcpctl.Fails(t, harness.ExitError, "deployment", "interfaces", "configure", "lo0", "--mtu", "1500")
		pcpctl.Fails(t, harness.ExitUsage, "deployment", "interfaces", "configure", "lo0")
	})
	t.Run("packages", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "deployment", "packages", "add", "/nonexistent/package.ptar")
		pcpctl.Fails(t, harness.ExitUsage, "deployment", "packages", "add")
	})
}
