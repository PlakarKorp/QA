// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv1

import (
	"strings"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

// TestSettingSections lists the sections the deployment holds, each with
// its scope.
func TestSettingSections(t *testing.T) {
	requireInstance(t)

	var sections struct {
		Scope    string `json:"scope"`
		Sections []struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"sections"`
	}
	pcpctl.JSON(t, &sections, "setting", "sections", "--deployment")
	require.Equal(t, "deployment", sections.Scope)
	var names []string
	for _, s := range sections.Sections {
		names = append(names, s.Name)
		require.Equal(t, "deployment", s.Scope)
	}
	require.Contains(t, names, "packages")
}

// TestSettingLifecycle sets a section on a sub-organization, reads it back
// as its own and as the effective value, unsets it, and sees the inherited
// value apply again.
func TestSettingLifecycle(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	// A section reads back wrapped in its name: {"retention": {...}}.
	type sections map[string]map[string]any

	var own sections
	in.JSON(t, &own, "setting", "list")
	require.Empty(t, own, "a new organization holds no section of its own")
	in.Fails(t, harness.ExitNotFound, "setting", "get", "retention")

	in.OK(t, "setting", "set", "retention", `{"min_versions": 7}`)

	in.JSON(t, &own, "setting", "get", "retention")
	require.EqualValues(t, 7, own["retention"]["min_versions"])

	// The effective value says where it came from.
	var effective struct {
		Setting sections `json:"setting"`
		Source  struct {
			Inherited      bool   `json:"inherited"`
			OrganizationID string `json:"organization_id"`
		} `json:"source"`
	}
	in.JSON(t, &effective, "setting", "get", "retention", "--effective")
	require.EqualValues(t, 7, effective.Setting["retention"]["min_versions"])
	require.False(t, effective.Source.Inherited, "the organization's own value is the effective one")
	require.Equal(t, child.ID, effective.Source.OrganizationID)

	in.JSON(t, &own, "setting", "list")
	require.Contains(t, own, "retention")

	in.Fails(t, harness.ExitError, "setting", "unset", "retention")
	in.OK(t, "setting", "unset", "retention", "--yes")
	var left sections
	in.JSON(t, &left, "setting", "list")
	require.Empty(t, left, "the organization's own value is gone")
	in.Fails(t, harness.ExitNotFound, "setting", "get", "retention")
}

// TestSettingMisuse: a section that does not exist, a value that is not JSON,
// and a value the section refuses.
func TestSettingMisuse(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	t.Run("unknown section", func(t *testing.T) {
		// Refused, though not as a not-found: the server answers 501 for a
		// section name it does not know, which pcpctl reports as a plain
		// failure.
		in.Fails(t, harness.ExitError, "setting", "get", "frobnication")
		in.Fails(t, harness.ExitError, "setting", "set", "frobnication", `{"on": true}`)
	})
	t.Run("not JSON", func(t *testing.T) {
		in.Fails(t, harness.ExitError, "setting", "set", "retention", `{min_versions: 7`)
	})
	t.Run("wrong shape", func(t *testing.T) {
		in.Fails(t, harness.ExitError, "setting", "set", "retention", `{"min_versions": "seven"}`)
	})
	t.Run("no arguments", func(t *testing.T) {
		in.Fails(t, harness.ExitUsage, "setting", "set", "retention")
	})
}

// TestQuota reads the organization's quota and sets a soft limit on it.
func TestQuota(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)
	if in.NotOnV2(t, "quota", "get") {
		return
	}

	var quota struct {
		OrganizationID string `json:"organization_id"`
		Stored         struct {
			Soft *int64 `json:"soft"`
			Used int64  `json:"used"`
		} `json:"stored"`
	}
	in.JSON(t, &quota, "quota", "get")
	require.Equal(t, child.ID, quota.OrganizationID)
	require.Nil(t, quota.Stored.Soft, "a new organization has no limit")

	in.OK(t, "quota", "set", "--stored-soft", "1048576")
	in.JSON(t, &quota, "quota", "get")
	require.NotNil(t, quota.Stored.Soft)
	require.EqualValues(t, 1048576, *quota.Stored.Soft)

	t.Run("nothing to set", func(t *testing.T) {
		in.OK(t, "quota", "set")
		in.JSON(t, &quota, "quota", "get")
		require.EqualValues(t, 1048576, *quota.Stored.Soft, "a set of nothing changes nothing")
	})
	t.Run("the deployment root", func(t *testing.T) {
		// The root's quota is its licence, so a write there is a conflict.
		pcpctl.Fails(t, harness.ExitConflict, "quota", "set", "--stored-soft", "1")
	})
}

// TestResidencies lists the countries a residency may name, with the EU
// filter narrowing it.
func TestResidencies(t *testing.T) {
	requireInstance(t)

	var all []struct {
		Alpha2 string `json:"alpha2"`
		EU     bool   `json:"eu"`
	}
	pcpctl.JSON(t, &all, "catalogue", "residencies")
	var eu int
	for _, c := range all {
		require.Len(t, c.Alpha2, 2)
		if c.EU {
			eu++
		}
	}
	require.NotZero(t, eu)
	require.Less(t, eu, len(all))

	// The filter is applied to the table; a header plus one row per country.
	rows := strings.Count(strings.TrimSpace(pcpctl.OK(t, "catalogue", "residencies", "--eu")), "\n")
	require.Equal(t, eu, rows, "--eu keeps the EU members and nothing else")
}

// TestResourceClasses lists the static class tree.
func TestResourceClasses(t *testing.T) {
	requireInstance(t)

	var classes map[string][]string
	pcpctl.JSON(t, &classes, "catalogue", "resource-classes")
	require.Contains(t, classes, "compute")
	require.Contains(t, classes, "object-storage")
}

// TestDataClassLifecycle creates a data class, lists it, deletes it, and
// refuses the duplicate and the deletion of nothing.
func TestDataClassLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-class")

	pcpctl.OK(t, "catalogue", "data-classes", "create", name)
	t.Cleanup(func() { pcpctl.Run(t, "catalogue", "data-classes", "delete", name, "--yes") })

	var classes []string
	pcpctl.JSON(t, &classes, "catalogue", "data-classes", "list")
	require.Contains(t, classes, name)

	// Creating a class that exists is idempotent rather than a conflict.
	pcpctl.OK(t, "catalogue", "data-classes", "create", name)
	pcpctl.JSON(t, &classes, "catalogue", "data-classes", "list")
	require.Equal(t, 1, count(classes, name))

	pcpctl.Fails(t, harness.ExitError, "catalogue", "data-classes", "delete", name)
	pcpctl.OK(t, "catalogue", "data-classes", "delete", name, "--yes")
	pcpctl.JSON(t, &classes, "catalogue", "data-classes", "list")
	require.NotContains(t, classes, name)

	pcpctl.Fails(t, harness.ExitNotFound, "catalogue", "data-classes", "delete", name, "--yes")
}

// TestEnvironmentLifecycle creates an environment, lists it, deletes it, and
// refuses the duplicate and the deletion of nothing.
func TestEnvironmentLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-env")

	pcpctl.OK(t, "catalogue", "environments", "create", name)
	t.Cleanup(func() { pcpctl.Run(t, "catalogue", "environments", "delete", name, "--yes") })

	var envs []string
	pcpctl.JSON(t, &envs, "catalogue", "environments", "list")
	require.Contains(t, envs, name)

	pcpctl.OK(t, "catalogue", "environments", "create", name)
	pcpctl.JSON(t, &envs, "catalogue", "environments", "list")
	require.Equal(t, 1, count(envs, name))

	pcpctl.Fails(t, harness.ExitError, "catalogue", "environments", "delete", name)
	pcpctl.OK(t, "catalogue", "environments", "delete", name, "--yes")
	pcpctl.JSON(t, &envs, "catalogue", "environments", "list")
	require.NotContains(t, envs, name)

	pcpctl.Fails(t, harness.ExitNotFound, "catalogue", "environments", "delete", name, "--yes")
}

func count(items []string, want string) int {
	var n int
	for _, item := range items {
		if item == want {
			n++
		}
	}
	return n
}

// TestSettingsExtras: the sections an organization may hold, several of
// them written at once, how one is handed down the subtree, and the
// deployment's own configuration beside it.
func TestSettingsExtras(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	var sections struct {
		Scope    string `json:"scope"`
		Sections []struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"sections"`
	}
	in.JSON(t, &sections, "setting", "sections")
	require.Equal(t, "organization", sections.Scope)
	var names []string
	for _, s := range sections.Sections {
		names = append(names, s.Name)
	}
	require.Contains(t, names, "retention")
	require.NotContains(t, names, "packages", "the deployment's sections are not the organization's")

	type stored map[string]map[string]any
	var written stored
	in.JSON(t, &written, "setting", "update", `{"retention":{"min_versions":3}}`)
	require.EqualValues(t, 3, written["retention"]["min_versions"])
	var own stored
	in.JSON(t, &own, "setting", "get", "retention")
	require.EqualValues(t, 3, own["retention"]["min_versions"])
	in.Fails(t, harness.ExitError, "setting", "update", "{nope")
	if !harness.OnV2() {
		// v2 accepts this and writes the deployment's package feed
		// (api/README.md); not run there, as every install after it would
		// go to that URL.
		in.Fails(t, harness.ExitError, "setting", "update", `{"packages":{"repository_url":"http://x"}}`)
	}

	t.Run("flags", func(t *testing.T) {
		in.JSON(t, &own, "setting", "flags", "retention", "--inheritable")
		require.EqualValues(t, 3, own["retention"]["min_versions"], "the flags answer the section as it stands")
		in.OK(t, "setting", "flags", "retention")
		in.Fails(t, harness.ExitError, "setting", "flags", "frobnication", "--locked")
		if !harness.OnV2() {
			// Only a section the organization holds can be handed down.
			in.Fails(t, harness.ExitNotFound, "setting", "flags", "mailer", "--inheritable")
		}
	})
	t.Run("deployment", func(t *testing.T) {
		pcpctl.JSON(t, &sections, "setting", "sections", "--deployment")
		require.Equal(t, "deployment", sections.Scope)
		var names []string
		for _, s := range sections.Sections {
			names = append(names, s.Name)
			require.Equal(t, "deployment", s.Scope)
		}
		require.Contains(t, names, "packages")
		require.Contains(t, names, "logs")

		// The log retention is the one deployment section a write leaves no
		// mark on: the packages feed would redirect every install after it.
		var all stored
		pcpctl.JSON(t, &all, "setting", "list", "--deployment")
		pcpctl.OK(t, "setting", "set", "--deployment", "logs", `{"retention_days":30}`)
		if harness.OnV2() {
			// v2 takes the write and then cannot show it: api/README.md.
			pcpctl.Fails(t, harness.ExitNotFound, "setting", "get", "--deployment", "logs")
			return
		}
		var logs stored
		pcpctl.JSON(t, &logs, "setting", "get", "--deployment", "logs")
		require.EqualValues(t, 30, logs["logs"]["retention_days"])
		pcpctl.JSON(t, &written, "setting", "update", "--deployment", `{"logs":{"retention_days":31}}`)
		require.EqualValues(t, 31, written["logs"]["retention_days"])
		pcpctl.JSON(t, &all, "setting", "list", "--deployment")
		require.Contains(t, all, "logs")

		pcpctl.Fails(t, harness.ExitError, "setting", "set", "--deployment", "retention", `{"min_versions":1}`)
		pcpctl.Fails(t, harness.ExitError, "setting", "update", "--deployment", `{"retention":{"min_versions":1}}`)
		pcpctl.Fails(t, harness.ExitError, "setting", "get", "--deployment", "retention", "--effective")
	})
	t.Run("search", func(t *testing.T) {
		var classes []string
		pcpctl.JSON(t, &classes, "catalogue", "data-classes", "search", "business")
		require.Contains(t, classes, "business_data")
		var envs []string
		pcpctl.JSON(t, &envs, "catalogue", "environments", "search", "produc")
		require.Equal(t, []string{"production"}, envs)
		pcpctl.JSON(t, &envs, "catalogue", "environments", "search", harness.Unique("no-such-env"))
		require.Empty(t, envs)
		pcpctl.Fails(t, harness.ExitUsage, "catalogue", "environments", "search")
	})
}
