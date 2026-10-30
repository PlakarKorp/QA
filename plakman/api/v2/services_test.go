// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type integration struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Supports  []string `json:"supports"`
	IsCurrent bool     `json:"is_current"`
}

// TestIntegrationCatalogue: s3 is installed and current, resolvable by name,
// readable by id, and what is not installed is a not-found. v1 also lists
// the catalogue of packages the feed offers and installs one by name and
// version; v2 installs from an uploaded package and has no catalogue.
func TestIntegrationCatalogue(t *testing.T) {
	requireInstance(t)

	installed := harness.List[integration](t, v2, "/api/v2/integrations?name=s3")
	require.Len(t, installed.Items, 1, "the rig installs s3")
	s3 := installed.Items[0]
	require.True(t, s3.IsCurrent)
	require.Contains(t, s3.Supports, "store")

	var resolved integration
	v2.OK(t, http.MethodGet, "/api/v2/integrations/resolve?name=s3", nil, &resolved)
	require.Equal(t, s3.ID, resolved.ID)
	v2.OK(t, http.MethodGet, "/api/v2/integrations/resolve?name=s3&supports=store", nil, &resolved)
	require.Equal(t, s3.ID, resolved.ID)

	var byID integration
	v2.OK(t, http.MethodGet, "/api/v2/integrations/"+s3.ID, nil, &byID)
	require.Equal(t, s3.Version, byID.Version)

	t.Run("absent", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/integrations/resolve?name=nope", nil, harness.CodeNotFound)
		v2.Refused(t, http.MethodGet, "/api/v2/integrations/"+missing, nil, harness.CodeNotFound)
		v2.Refused(t, http.MethodDelete, "/api/v2/integrations/"+missing, nil, harness.CodeNotFound)
		v2.Refused(t, http.MethodPost, "/api/v2/integrations/"+missing+"/actions/set-current", nil, harness.CodeNotFound)
	})
	t.Run("resolve without a name", func(t *testing.T) {
		finding(t, "GET /integrations/resolve without name answers 400 in fuego's {title,status,detail} shape rather than the v2 envelope")
		v2.Refused(t, http.MethodGet, "/api/v2/integrations/resolve", nil, harness.CodeInvalidArgument)
	})
	t.Run("catalogue", func(t *testing.T) {
		gap(t, "list the catalogue of packages the feed offers, or install one by name and version (v1 GET /integrations/catalog, POST /integrations/installed; pcpctl integration catalog/install)")
	})
}

// TestSecretProviders: none are seeded, and every read of one that does not
// exist is a not-found.
func TestSecretProviders(t *testing.T) {
	requireInstance(t)

	vaults := harness.List[any](t, v2, "/api/v2/secret-providers")
	require.Equal(t, len(vaults.Items), vaults.Total)

	v2.Refused(t, http.MethodGet, "/api/v2/secret-providers/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodDelete, "/api/v2/secret-providers/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/secret-providers/"+missing+"/actions/test", nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPatch, "/api/v2/secret-providers/"+missing, map[string]any{"name": "ghost"}, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/secret-providers", map[string]any{"type": "nope", "name": "x", "protocol": "x"}, harness.CodeInvalidArgument)
}

// TestBundles: a bundle is created with its match filter, read back, found
// in the listing, and deleted; the refusals are not-founds.
func TestBundles(t *testing.T) {
	requireInstance(t)

	bundles := harness.List[any](t, v2, "/api/v2/bundles")
	require.Equal(t, len(bundles.Items), bundles.Total)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))
	require.Empty(t, harness.List[any](t, v2, "/api/v2/bundles?inventory_id="+inv.ID).Items)

	v2.Refused(t, http.MethodGet, "/api/v2/bundles/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodDelete, "/api/v2/bundles/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/bundles", map[string]any{"resource_classes": []string{"object-storage"}}, harness.CodeInvalidArgument)

	t.Run("with fields", func(t *testing.T) {
		finding(t, "POST /bundles refuses any fields as invalid_argument, as v1 does, though the v2 contract declares them")
		v2.OK(t, http.MethodPost, "/api/v2/bundles", map[string]any{
			"integration": "s3", "resource_classes": []string{"object-storage"},
			"fields": []map[string]string{{"key": "root", "value": "/backups"}},
		}, nil)
	})
	t.Run("create", func(t *testing.T) {
		var b struct {
			ID              string   `json:"id"`
			Integration     string   `json:"integration"`
			ResourceClasses []string `json:"resource_classes"`
		}
		v2.OK(t, http.MethodPost, "/api/v2/bundles", map[string]any{
			"integration": "s3", "resource_classes": []string{"object-storage"},
		}, &b)
		require.NotEmpty(t, b.ID)
		require.Equal(t, "s3", b.Integration)
		require.Equal(t, []string{"object-storage"}, b.ResourceClasses)
		t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/bundles/"+b.ID, nil) })

		var got struct {
			ID string `json:"id"`
		}
		v2.OK(t, http.MethodGet, "/api/v2/bundles/"+b.ID, nil, &got)
		require.Equal(t, b.ID, got.ID)

		status, _ := v2.Do(t, http.MethodDelete, "/api/v2/bundles/"+b.ID, nil)
		require.Equal(t, http.StatusNoContent, status)
		v2.Refused(t, http.MethodGet, "/api/v2/bundles/"+b.ID, nil, harness.CodeNotFound)
	})
}

// TestSLA: nothing is contracted on a fresh instance, the reads answer their
// empty pages, and a template or contract that does not exist is a
// not-found.
func TestSLA(t *testing.T) {
	requireInstance(t)

	templates := harness.List[any](t, v2, "/api/v2/sla-templates")
	require.Equal(t, len(templates.Items), templates.Total)
	contracts := harness.List[any](t, v2, "/api/v2/sla-contracts")
	require.Equal(t, len(contracts.Items), contracts.Total)
	harness.List[any](t, v2, "/api/v2/reports/sla-matching-resources")

	v2.Refused(t, http.MethodGet, "/api/v2/sla-templates/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodDelete, "/api/v2/sla-templates/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPatch, "/api/v2/sla-templates/"+missing, map[string]any{"name": "ghost"}, harness.CodeNotFound)
	// Well-formed ids that name nothing are reported as a bad argument here.
	v2.Refused(t, http.MethodPost, "/api/v2/sla-contracts", map[string]any{"template_id": missing, "source_id": missing}, harness.CodeInvalidArgument)
	v2.Refused(t, http.MethodDelete, "/api/v2/sla-contracts/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/sla-contracts", map[string]any{"template_id": missing}, harness.CodeInvalidArgument)
}

type scheduler struct {
	ID      string `json:"id"`
	Actor   string `json:"actor"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func defaultScheduler(t *testing.T, c *harness.V2) scheduler {
	t.Helper()
	for _, s := range harness.List[scheduler](t, c, "/api/v2/schedulers?actor=default").Items {
		if s.Actor == "default" {
			return s
		}
	}
	t.Fatal("no default scheduler")
	return scheduler{}
}

// TestScheduler: every organization has a default scheduler from birth, with
// no tasks, no user schedulers, an empty role list, and nothing planned.
func TestScheduler(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	s := defaultScheduler(t, in)
	require.NotEmpty(t, s.ID)
	require.True(t, s.Enabled)
	var got scheduler
	in.OK(t, http.MethodGet, "/api/v2/schedulers/"+s.ID, nil, &got)
	require.Equal(t, s, got)

	require.Empty(t, harness.List[any](t, in, "/api/v2/scheduler-tasks?scheduler_id="+s.ID).Items)
	require.Empty(t, harness.List[any](t, in, "/api/v2/schedulers?actor=user").Items, "nobody has a scheduler of their own yet")
	window := "from=" + time.Now().UTC().Format(time.RFC3339) + "&to=" + time.Now().UTC().Add(7*24*time.Hour).Format(time.RFC3339)
	require.Empty(t, harness.List[any](t, in, "/api/v2/reports/planned-jobs?"+window).Items)

	in.Refused(t, http.MethodPost, "/api/v2/scheduler-tasks/"+missing+"/actions/run", map[string]any{}, harness.CodeNotFound)
	in.Refused(t, http.MethodDelete, "/api/v2/scheduler-tasks/"+missing, nil, harness.CodeNotFound)
	in.Refused(t, http.MethodGet, "/api/v2/schedulers/"+missing, nil, harness.CodeNotFound)
	in.Refused(t, http.MethodDelete, "/api/v2/schedulers/"+s.ID, nil, harness.CodeConflict)
}

// TestSchedulerRoles sets who may read and execute on the scheduler with a
// patch and reads it back; an unknown token is refused and changes nothing.
func TestSchedulerRoles(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)
	s := defaultScheduler(t, in)

	var g group
	in.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": harness.Unique("qa-readers")}, &g)
	t.Cleanup(func() { in.Do(t, http.MethodDelete, "/api/v2/groups/"+g.ID, nil) })
	token := "group:" + g.ID

	var roles struct {
		Read    []string `json:"read"`
		Execute []string `json:"execute"`
	}
	status, raw := in.Do(t, http.MethodPatch, "/api/v2/schedulers/"+s.ID+"/roles", map[string]any{"read": []string{token}, "execute": []string{"wheel"}})
	require.Equal(t, http.StatusOK, status, "%s", raw)
	in.OK(t, http.MethodPatch, "/api/v2/schedulers/"+s.ID+"/roles", map[string]any{}, &roles)
	require.Equal(t, []string{token}, roles.Read)
	require.Equal(t, []string{"wheel"}, roles.Execute)

	t.Run("unknown token", func(t *testing.T) {
		finding(t, "PATCH /schedulers/{id}/roles accepts a token that is neither a group nor a built-in ('emperor'), as v1 does; it should be a 400")
		in.Refused(t, http.MethodPatch, "/api/v2/schedulers/"+s.ID+"/roles", map[string]any{"read": []string{"emperor"}}, harness.CodeInvalidArgument)
		in.OK(t, http.MethodPatch, "/api/v2/schedulers/"+s.ID+"/roles", map[string]any{}, &roles)
		require.Equal(t, []string{token}, roles.Read, "a refused change changes nothing")
	})
}

// TestAuditTrail: what the scenarios did is in the audit log, the export is
// signed JSONL, and the attestations carry a trailer of their own.
func TestAuditTrail(t *testing.T) {
	requireInstance(t)
	org := createOrg(t, v2, harness.Unique("qa-audited"), "")

	// An organization's creation is an entry of its own log, not of its
	// parent's: the organization is a filter on the listing.
	in := scoped(t, v2, org.ID)
	entries := harness.List[struct {
		Event          string `json:"event"`
		ActorUserID    string `json:"actor_user_id"`
		OrganizationID string `json:"organization_id"`
	}](t, v2, "/api/v2/audit-logs?limit=200&organization_id="+org.ID)
	var first struct {
		Event          string `json:"event"`
		ActorUserID    string `json:"actor_user_id"`
		OrganizationID string `json:"organization_id"`
	}
	for _, e := range entries.Items {
		require.Equal(t, org.ID, e.OrganizationID, "the filter keeps one organization's entries")
		if e.Event == "organization.created" {
			first = e
		}
	}
	require.Equal(t, "organization.created", first.Event, "the creation is in the log")
	require.Equal(t, org.ID, first.OrganizationID)
	t.Run("actor", func(t *testing.T) {
		finding(t, "the organization.created entry in the new organization's log names no actor, as in v1")
		require.Equal(t, inst.UserID.String(), first.ActorUserID, "logged against us")
	})

	// The default export continues from the organization's export head, which
	// a new organization has never advanced, so it holds the whole log.
	status, export := in.Do(t, http.MethodPost, "/api/v2/audit-logs/actions/export", map[string]any{"format": "jsonl"})
	require.Equal(t, http.StatusOK, status, "%s", export)
	require.Contains(t, string(export), `"event":"organization.created"`)
	require.Contains(t, string(export), `"seq":`)
	require.Contains(t, string(export), `"trailer":true`, "the export is sealed")

	keys := harness.List[struct {
		Purpose   string `json:"purpose"`
		Algorithm string `json:"algorithm"`
		PublicKey string `json:"public_key"`
	}](t, v2, "/api/v2/signing-keys?purpose=audit")
	require.Len(t, keys.Items, 1)
	require.Equal(t, "ed25519", keys.Items[0].Algorithm)
	require.True(t, strings.HasPrefix(keys.Items[0].PublicKey, "ssh-ed25519 "))

	t.Run("attestations", func(t *testing.T) {
		for kind, attests := range map[string]string{"residency": "residency", "legal-hold": "legal_hold"} {
			status, out := v2.Do(t, http.MethodGet, "/api/v2/reports/"+kind+"-attestation", nil)
			require.Equal(t, http.StatusOK, status, "%s", out)
			require.Contains(t, string(out), `"trailer":true`)
			require.Contains(t, string(out), `"attests":"`+attests+`"`)
			require.Len(t, harness.List[any](t, v2, "/api/v2/signing-keys?purpose="+attests).Items, 1)
		}
		status, _ := v2.Do(t, http.MethodGet, "/api/v2/reports/residency-attestation?format=csv", nil)
		require.Equal(t, http.StatusBadRequest, status)
	})
	t.Run("bad filters", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/audit-logs?since=yesterday", nil, harness.CodeInvalidArgument)
		v2.Refused(t, http.MethodPost, "/api/v2/audit-logs/actions/export", map[string]any{"format": "xml"}, harness.CodeInvalidArgument)
	})
}

// TestDeployment: the reads of the deployment itself answer, and the writes
// that need a licence or a real update refuse. v1 also exposes the debug log
// switch, the network interfaces and the licence upload, which v2 does not.
func TestDeployment(t *testing.T) {
	requireInstance(t)

	var system struct {
		Version  string `json:"version"`
		Enrolled bool   `json:"enrolled"`
		SafeMode bool   `json:"safe_mode"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/system", nil, &system)
	require.NotEmpty(t, system.Version)
	require.True(t, system.Enrolled)
	require.False(t, system.SafeMode)

	var update struct {
		HasUpdate      bool   `json:"has_update"`
		CurrentVersion string `json:"current_version"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/system/update", nil, &update)
	require.False(t, update.HasUpdate, "the mock orkestrator offers no update")
	require.Equal(t, system.Version, update.CurrentVersion)

	var report struct {
		Version      string `json:"version"`
		Integrations []struct {
			Name string `json:"name"`
		} `json:"integrations"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/system/report", nil, &report)
	var names []string
	for _, i := range report.Integrations {
		names = append(names, i.Name)
	}
	require.Contains(t, names, "s3", "the report names what is installed")

	var activity map[string]any
	v2.OK(t, http.MethodGet, "/api/v2/reports/activity-summary", nil, &activity)
	require.NotEmpty(t, activity)

	t.Run("refusals", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/system/actions/update", map[string]any{"next_version": "v0.0.0"}, harness.CodeInvalidArgument)
		v2.Refused(t, http.MethodPost, "/api/v2/system/actions/update", map[string]any{}, harness.CodeInvalidArgument)
	})
	t.Run("debug log", func(t *testing.T) {
		gap(t, "read or switch the deployment's debug logging (v1 GET/PUT /debuglog, pcpctl deployment debug-log)")
	})
	t.Run("network interfaces", func(t *testing.T) {
		gap(t, "list the appliance's network interfaces (v1 GET /network/interfaces, pcpctl deployment interfaces)")
	})
	t.Run("licence", func(t *testing.T) {
		gap(t, "replace the deployment's licence (v1 PUT /license, pcpctl deployment license)")
	})
}

// TestEdges: no edge is enrolled, and an enrollment key can be minted.
func TestEdges(t *testing.T) {
	requireInstance(t)

	require.Empty(t, harness.List[any](t, v2, "/api/v2/edges").Items)
	v2.Refused(t, http.MethodGet, "/api/v2/edges/"+missing, nil, harness.CodeNotFound)

	var key struct {
		EnrollmentKey string `json:"enrollment_key"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/edges/actions/generate-enrollment-key", nil, &key)
	require.NotEmpty(t, key.EnrollmentKey)
}

// TestUsage: a request that cannot be understood is refused with the
// envelope and the documented code before anything is attempted: a route
// that does not exist, an id that is not one, a verb the route has no
// handler for.
func TestUsage(t *testing.T) {
	requireInstance(t)

	v2.Refused(t, http.MethodGet, "/api/v2/frobnicate", nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodGet, "/api/v2/organizations/not-an-id", nil, harness.CodeInvalidArgument)
	v2.Refused(t, http.MethodGet, "/api/v2/organizations?parent_id=not-an-id", nil, harness.CodeInvalidArgument)
	status, _ := v2.Do(t, http.MethodPut, "/api/v2/organizations", nil)
	require.Contains(t, []int{http.StatusMethodNotAllowed, http.StatusNotFound}, status)
}
