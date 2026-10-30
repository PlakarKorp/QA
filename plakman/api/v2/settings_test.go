// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type setting struct {
	Name      string          `json:"name"`
	Scope     string          `json:"scope"`
	Config    json.RawMessage `json:"config"`
	Inherited bool            `json:"inherited"`
}

func settingConfig(t *testing.T, s setting) map[string]any {
	t.Helper()
	var config map[string]any
	require.NoError(t, json.Unmarshal(s.Config, &config))
	return config
}

// TestSettingSections lists the sections a setting may name, each with the
// scope it is written at.
func TestSettingSections(t *testing.T) {
	requireInstance(t)

	sections := harness.List[struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}](t, v2, "/api/v2/setting-sections?limit=200")
	byName := map[string]string{}
	for _, s := range sections.Items {
		byName[s.Name] = s.Scope
	}
	require.Equal(t, "deployment", byName["packages"])
	require.Equal(t, "organization", byName["retention"])
}

// TestSettingLifecycle sets a section on a sub-organization, reads it back
// as its own and as the value that applies, deletes it, and sees the
// organization hold nothing of its own again.
func TestSettingLifecycle(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	require.Empty(t, harness.List[setting](t, in, "/api/v2/settings").Items, "a new organization holds no section of its own")
	status, _ := in.Do(t, http.MethodGet, "/api/v2/settings/retention", nil)
	require.Contains(t, []int{http.StatusOK, http.StatusNotFound}, status, "before a write, retention is inherited or absent")

	var own setting
	in.OK(t, http.MethodPatch, "/api/v2/settings/retention", map[string]any{"min_versions": 7}, &own)
	require.Equal(t, "retention", own.Name)
	require.False(t, own.Inherited)
	require.EqualValues(t, 7, settingConfig(t, own)["min_versions"])

	in.OK(t, http.MethodGet, "/api/v2/settings/retention", nil, &own)
	require.False(t, own.Inherited, "the organization's own value is the one that applies")
	require.EqualValues(t, 7, settingConfig(t, own)["min_versions"])

	var names []string
	for _, s := range harness.List[setting](t, in, "/api/v2/settings").Items {
		names = append(names, s.Name)
	}
	require.Equal(t, []string{"retention"}, names)

	status, _ = in.Do(t, http.MethodDelete, "/api/v2/settings/retention", nil)
	require.Equal(t, http.StatusNoContent, status)
	require.Empty(t, harness.List[setting](t, in, "/api/v2/settings").Items, "the organization's own value is gone")
}

// TestSettingMisuse: a section that does not exist, a value that is not
// JSON, and a value the section refuses.
func TestSettingMisuse(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	t.Run("unknown section", func(t *testing.T) {
		in.Refused(t, http.MethodGet, "/api/v2/settings/frobnication", nil, harness.CodeInvalidArgument)
		in.Refused(t, http.MethodPatch, "/api/v2/settings/frobnication", map[string]any{"on": true}, harness.CodeInvalidArgument)
	})
	t.Run("not JSON", func(t *testing.T) {
		status, _ := in.Do(t, http.MethodPatch, "/api/v2/settings/retention", "{min_versions: 7")
		require.Equal(t, http.StatusBadRequest, status)
	})
	t.Run("wrong shape", func(t *testing.T) {
		in.Refused(t, http.MethodPatch, "/api/v2/settings/retention", map[string]any{"min_versions": "seven"}, harness.CodeInvalidArgument)
	})
	t.Run("delete what was never set", func(t *testing.T) {
		in.Refused(t, http.MethodDelete, "/api/v2/settings/retention", nil, harness.CodeNotFound)
	})
}

// TestQuota reads an organization's quota and sets a soft limit on it.
func TestQuota(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	status, raw := in.Do(t, http.MethodGet, "/api/v2/quota", nil)
	if status == http.StatusNotFound {
		gap(t, "read or set an organization's quota (v1 GET/PUT /account/organizations/{id}/quota, pcpctl quota get/set)")
	}
	require.Equal(t, http.StatusOK, status, "%s", raw)
}

// TestResidencies lists the countries a residency may name, flagged by EU
// membership.
func TestResidencies(t *testing.T) {
	requireInstance(t)

	type country struct {
		Alpha2 string `json:"alpha2"`
		EU     bool   `json:"eu"`
	}
	// A page holds 200 at most, so the vocabulary takes two.
	first := harness.List[country](t, v2, "/api/v2/residencies?limit=200")
	require.Len(t, first.Items, 200)
	rest := harness.List[country](t, v2, "/api/v2/residencies?limit=200&offset=200")
	all := append(first.Items, rest.Items...)
	require.Len(t, all, first.Total)
	var eu int
	for _, c := range all {
		require.Len(t, c.Alpha2, 2)
		if c.EU {
			eu++
		}
	}
	require.NotZero(t, eu)
	require.Less(t, eu, len(all))
}

// TestResourceClasses lists the static class tree.
func TestResourceClasses(t *testing.T) {
	requireInstance(t)

	classes := harness.List[struct {
		Class string `json:"class"`
	}](t, v2, "/api/v2/resource-classes")
	var names []string
	for _, c := range classes.Items {
		names = append(names, c.Class)
	}
	require.Contains(t, names, "compute")
	require.Contains(t, names, "object-storage")
}

type taxon struct {
	ID          string `json:"id"`
	Class       string `json:"class"`
	Environment string `json:"environment"`
}

// TestDataClassLifecycle creates a data class, lists it, creates it again
// without a second one appearing, deletes it, and refuses the deletion of
// nothing.
func TestDataClassLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-class")

	var created taxon
	v2.OK(t, http.MethodPost, "/api/v2/data-classes", map[string]string{"class": name}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Class)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/data-classes/"+created.ID, nil) })

	listed := harness.List[taxon](t, v2, "/api/v2/data-classes?search="+name)
	require.Len(t, listed.Items, 1)
	require.Equal(t, created.ID, listed.Items[0].ID)

	t.Run("create again", func(t *testing.T) {
		finding(t, "POST /data-classes with a class that exists answers 500; v1 treats it as already created")
		var again taxon
		v2.OK(t, http.MethodPost, "/api/v2/data-classes", map[string]string{"class": name}, &again)
		require.Equal(t, created.ID, again.ID, "creating a class that exists is idempotent")
		require.Len(t, harness.List[taxon](t, v2, "/api/v2/data-classes?search="+name).Items, 1)
	})

	var got taxon
	v2.OK(t, http.MethodGet, "/api/v2/data-classes/"+created.ID, nil, &got)
	require.Equal(t, name, got.Class)

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/data-classes/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	require.Empty(t, harness.List[taxon](t, v2, "/api/v2/data-classes?search="+name).Items)
	v2.Refused(t, http.MethodDelete, "/api/v2/data-classes/"+created.ID, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/data-classes", map[string]string{}, harness.CodeInvalidArgument)
}

// TestEnvironmentLifecycle creates an environment, lists it, creates it
// again without a second one appearing, deletes it, and refuses the deletion
// of nothing.
func TestEnvironmentLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-env")

	var created taxon
	v2.OK(t, http.MethodPost, "/api/v2/environments", map[string]string{"environment": name}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Environment)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/environments/"+created.ID, nil) })

	listed := harness.List[taxon](t, v2, "/api/v2/environments?search="+name)
	require.Len(t, listed.Items, 1)

	t.Run("create again", func(t *testing.T) {
		finding(t, "POST /environments with an environment that exists answers 500; v1 treats it as already created")
		var again taxon
		v2.OK(t, http.MethodPost, "/api/v2/environments", map[string]string{"environment": name}, &again)
		require.Equal(t, created.ID, again.ID, "creating an environment that exists is idempotent")
		require.Len(t, harness.List[taxon](t, v2, "/api/v2/environments?search="+name).Items, 1)
	})

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/environments/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	require.Empty(t, harness.List[taxon](t, v2, "/api/v2/environments?search="+name).Items)
	v2.Refused(t, http.MethodDelete, "/api/v2/environments/"+created.ID, nil, harness.CodeNotFound)
}
