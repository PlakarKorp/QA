// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"encoding/json"
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

// TestSchedulerLifecycle: the caller makes a scheduler of their own beside
// the default one, cannot make a second, sees it among the user schedulers,
// deletes it, and cannot delete what is gone.
func TestSchedulerLifecycle(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	var s struct {
		ID          string `json:"id"`
		Actor       string `json:"actor"`
		OwnerUserID string `json:"owner_user_id"`
	}
	in.OK(t, http.MethodPost, "/api/v2/schedulers", map[string]string{"name": "Mine"}, &s)
	require.NotEmpty(t, s.ID)
	require.Equal(t, "user", s.Actor)
	require.Equal(t, inst.UserID.String(), s.OwnerUserID)
	t.Cleanup(func() { in.Do(t, http.MethodDelete, "/api/v2/schedulers/"+s.ID, nil) })

	t.Run("twice", func(t *testing.T) {
		finding(t, "POST /schedulers for a caller who already has one answers 500; v1 answers 409")
		in.Refused(t, http.MethodPost, "/api/v2/schedulers", map[string]string{"name": "Another"}, harness.CodeConflict)
	})
	in.Refused(t, http.MethodPost, "/api/v2/schedulers", map[string]string{}, harness.CodeInvalidArgument)

	users := harness.List[scheduler](t, in, "/api/v2/schedulers?actor=user")
	require.Len(t, users.Items, 1)
	require.Equal(t, s.ID, users.Items[0].ID)
	require.Equal(t, "default", defaultScheduler(t, in).Actor, "the default scheduler stays the organization's")

	status, _ := in.Do(t, http.MethodDelete, "/api/v2/schedulers/"+s.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	require.Empty(t, harness.List[scheduler](t, in, "/api/v2/schedulers?actor=user").Items)
	in.Refused(t, http.MethodDelete, "/api/v2/schedulers/"+s.ID, nil, harness.CodeNotFound)
}

type schedulerTask struct {
	ID          string `json:"id"`
	SchedulerID string `json:"scheduler_id"`
	Type        string `json:"type"`
	Origin      string `json:"origin"`
	Target      string `json:"target"`
}

func taskIDs(t *testing.T, c *harness.V2, schedulerID string) []string {
	t.Helper()
	page := harness.List[schedulerTask](t, c, "/api/v2/scheduler-tasks?limit=200&scheduler_id="+schedulerID)
	ids := make([]string, len(page.Items))
	for i, x := range page.Items {
		ids[i] = x.ID
	}
	return ids
}

// TestTaskLifecycle adds a check task on a store, finds it in the listing,
// patches it, runs it once, deletes it, and sees it gone; a task with
// nothing to run on, or of no type, is refused.
func TestTaskLifecycle(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, map[string]string{"key": "port", "value": "1"}, map[string]string{"key": "use_tls", "value": "false"})
	s := defaultScheduler(t, v2)

	var created schedulerTask
	v2.OK(t, http.MethodPost, "/api/v2/scheduler-tasks", map[string]any{"scheduler_id": s.ID, "type": "check", "origin": c.ID}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "check", created.Type)
	require.Equal(t, c.ID, created.Origin)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/scheduler-tasks/"+created.ID, nil) })
	require.Contains(t, taskIDs(t, v2, s.ID), created.ID)

	var got schedulerTask
	v2.OK(t, http.MethodGet, "/api/v2/scheduler-tasks/"+created.ID, nil, &got)
	require.Equal(t, created.ID, got.ID)
	v2.OK(t, http.MethodPatch, "/api/v2/scheduler-tasks/"+created.ID, map[string]any{"config": map[string]string{}}, &got)
	require.Equal(t, created.ID, got.ID, "a patch keeps the task's id")

	var run struct {
		RunID string `json:"run_id"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/scheduler-tasks/"+created.ID+"/actions/run", map[string]any{}, &run)
	require.NotEmpty(t, run.RunID)

	t.Run("refusals", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/scheduler-tasks", map[string]any{"scheduler_id": s.ID, "origin": c.ID}, harness.CodeInvalidArgument)
		// Unlike v1, v2 takes a task with nothing to run on.
		var bare schedulerTask
		v2.OK(t, http.MethodPost, "/api/v2/scheduler-tasks", map[string]any{"scheduler_id": s.ID, "type": "check"}, &bare)
		v2.OK(t, http.MethodDelete, "/api/v2/scheduler-tasks/"+bare.ID, nil, nil)
		v2.Refused(t, http.MethodPost, "/api/v2/scheduler-tasks", map[string]any{"scheduler_id": s.ID, "type": "check", "origin": missing}, harness.CodeNotFound)
		v2.Refused(t, http.MethodPatch, "/api/v2/scheduler-tasks/"+missing, map[string]any{"type": "check"}, harness.CodeNotFound)
	})

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/scheduler-tasks/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	require.NotContains(t, taskIDs(t, v2, s.ID), created.ID)
	v2.Refused(t, http.MethodDelete, "/api/v2/scheduler-tasks/"+created.ID, nil, harness.CodeNotFound)
}

// TestOneOffRun: v2 runs a task that exists, so a one-off run is a task made
// for it and run at once; the run is read back by its id.
func TestOneOffRun(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, map[string]string{"key": "port", "value": "1"}, map[string]string{"key": "use_tls", "value": "false"})
	s := defaultScheduler(t, v2)

	var task schedulerTask
	v2.OK(t, http.MethodPost, "/api/v2/scheduler-tasks", map[string]any{"scheduler_id": s.ID, "type": "check", "origin": c.ID}, &task)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/scheduler-tasks/"+task.ID, nil) })

	var run struct {
		RunID string `json:"run_id"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/scheduler-tasks/"+task.ID+"/actions/run", map[string]any{}, &run)
	require.NotEmpty(t, run.RunID)

	var got struct {
		RunID string `json:"run_id"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/runs/"+run.RunID, nil, &got)
	require.Equal(t, run.RunID, got.RunID)

	v2.Refused(t, http.MethodGet, "/api/v2/runs/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/scheduler-tasks/"+missing+"/actions/run", map[string]any{}, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/scheduler-tasks/"+task.ID+"/actions/run", map[string]any{"run_at": "tomorrow"}, harness.CodeInvalidArgument)
}

type subjectAccess struct {
	Read    []string `json:"read"`
	Execute []string `json:"execute"`
	Write   []string `json:"write"`
	Own     []string `json:"own"`
}

// TestSchedulerAccess sets who holds which verb on the scheduler class, as
// group tokens, and reads it back; v2 has no widening guard, and an empty
// patch clears nothing while explicit empty lists do.
func TestSchedulerAccess(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)
	var g group
	in.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": harness.Unique("qa-readers")}, &g)
	t.Cleanup(func() { in.Do(t, http.MethodDelete, "/api/v2/groups/"+g.ID, nil) })
	token := "group:" + g.ID

	var access subjectAccess
	in.OK(t, http.MethodGet, "/api/v2/subjects/scheduler/access", nil, &access)
	require.Empty(t, access.Read)

	in.OK(t, http.MethodPatch, "/api/v2/subjects/scheduler/access", map[string]any{"read": []string{token}, "execute": []string{token}}, &access)
	require.Equal(t, []string{token}, access.Read)
	require.Equal(t, []string{token}, access.Execute)
	in.OK(t, http.MethodGet, "/api/v2/subjects/scheduler/access", nil, &access)
	require.Equal(t, []string{token}, access.Read)

	in.OK(t, http.MethodPatch, "/api/v2/subjects/scheduler/access", map[string]any{"read": []string{}, "execute": []string{}}, &access)
	require.Empty(t, access.Read, "an explicit empty list clears the verb")

	subjects := harness.List[struct {
		Name string `json:"name"`
	}](t, in, "/api/v2/subjects")
	var names []string
	for _, s := range subjects.Items {
		names = append(names, s.Name)
	}
	require.Contains(t, names, "scheduler")
}

// TestClassAccess does the same for a class of resources: inventories here.
func TestClassAccess(t *testing.T) {
	requireInstance(t)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)
	var g group
	in.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": harness.Unique("qa-readers")}, &g)
	t.Cleanup(func() { in.Do(t, http.MethodDelete, "/api/v2/groups/"+g.ID, nil) })
	token := "group:" + g.ID

	var access subjectAccess
	in.OK(t, http.MethodGet, "/api/v2/subjects/inventory/access", nil, &access)
	require.Empty(t, access.Read)
	in.OK(t, http.MethodPatch, "/api/v2/subjects/inventory/access", map[string]any{"read": []string{token}}, &access)
	require.Equal(t, []string{token}, access.Read)
	in.OK(t, http.MethodGet, "/api/v2/subjects/inventory/access", nil, &access)
	require.Equal(t, []string{token}, access.Read)
	in.OK(t, http.MethodPatch, "/api/v2/subjects/inventory/access", map[string]any{"read": []string{}}, &access)
	require.Empty(t, access.Read)

	in.Refused(t, http.MethodGet, "/api/v2/subjects/frobnicate/access", nil, harness.CodeInvalidArgument)
}

// TestSchedulerJournal: v1 exports the scheduler's job journal as CSV; v2
// has the activity summary, a rollup, and no journal.
func TestSchedulerJournal(t *testing.T) {
	requireInstance(t)
	gap(t, "export the scheduler's job journal (v1 GET /scheduling/scheduler/journal, pcpctl scheduler journal)")
}

// TestSnapshotsOfSource: a source holds no snapshots on a fresh store, the
// reads of what is not there are not-founds, and a prune of an empty store
// previews and runs to nothing. Pruning is an action on the store connector.
func TestSnapshotsOfSource(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, map[string]string{"key": "port", "value": "1"}, map[string]string{"key": "use_tls", "value": "false"})
	var src connector
	v2.OK(t, http.MethodPost, "/api/v2/connectors", map[string]any{
		"type": "source", "integration": "s3", "name": harness.Unique("qa-source"), "protocol": "s3",
		"urn_id": r.URNID, "fields": s3Fields,
	}, &src)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/connectors/"+src.ID, nil) })

	require.Empty(t, harness.List[any](t, v2, "/api/v2/snapshots?source_id="+src.ID).Items)
	require.Empty(t, harness.List[any](t, v2, "/api/v2/snapshots?store_id="+c.ID).Items)
	v2.Refused(t, http.MethodGet, "/api/v2/snapshots/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/snapshots/"+missing+"/actions/place-legal-hold", map[string]any{}, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/snapshots/"+missing+"/actions/release-legal-hold", map[string]any{"store_id": c.ID}, harness.CodeNotFound)
	// A filter on a source that does not exist is an empty page, not a
	// refusal: the filter is a value, and no snapshot carries it.
	require.Empty(t, harness.List[any](t, v2, "/api/v2/snapshots?source_id="+missing).Items)

	t.Run("prune", func(t *testing.T) {
		var preview struct {
			WouldDelete []string `json:"would_delete"`
			WouldKeep   []string `json:"would_keep"`
		}
		v2.OK(t, http.MethodPost, "/api/v2/connectors/"+c.ID+"/actions/prune-preview", map[string]any{"config": map[string]string{"per_day": "1"}}, &preview)
		require.Empty(t, preview.WouldDelete)
		require.Empty(t, preview.WouldKeep)
		var run struct {
			Deleted []string `json:"deleted"`
		}
		v2.OK(t, http.MethodPost, "/api/v2/connectors/"+c.ID+"/actions/prune", map[string]any{"config": map[string]string{"per_day": "1"}}, &run)
		require.Empty(t, run.Deleted)
		v2.Refused(t, http.MethodPost, "/api/v2/connectors/"+missing+"/actions/prune-preview", map[string]any{"config": map[string]string{}}, harness.CodeNotFound)
	})
}

// TestConnectorExtras: v1 reads a connector's capabilities, its secrets and
// its configuration as form data; v2 has none of those, and the store behind
// a connector is an action that answers the executor's verdict.
func TestConnectorExtras(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, map[string]string{"key": "port", "value": "1"}, map[string]string{"key": "use_tls", "value": "false"})

	t.Run("capabilities", func(t *testing.T) {
		gap(t, "say what the caller may do to a connector (v1 GET /connectors/{id}/capabilities, pcpctl connector capabilities)")
	})
	t.Run("secrets", func(t *testing.T) {
		gap(t, "reveal a connector's sensitive values (v1 GET /connectors/{id}/secrets, pcpctl connector secrets)")
	})
	t.Run("form", func(t *testing.T) {
		gap(t, "read or write a connector as form data against its schema (v1 the /form routes, pcpctl connector form)")
	})
	t.Run("create-store", func(t *testing.T) {
		var result struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		v2.OK(t, http.MethodPost, "/api/v2/connectors/"+c.ID+"/actions/create-store", nil, &result)
		require.False(t, result.OK, "nothing listens where the store would be made")
		require.NotEmpty(t, result.Error)
		v2.Refused(t, http.MethodPost, "/api/v2/connectors/"+missing+"/actions/create-store", nil, harness.CodeNotFound)
	})
}

// TestBackupAndRestoreViews: v1 answers what protects each resource and what
// can be restored from where; v2 has the resources and the connectors, not
// the two views.
func TestBackupAndRestoreViews(t *testing.T) {
	requireInstance(t)
	gap(t, "list resources with the backups of their sources, or with a source holding a snapshot, or a source's restore points (v1 /backups and /restore routes, pcpctl backup and restore)")
}

// TestIntegrationDetail reads one integration by name and version, by id,
// and as the current version for a connector type; the form schema and the
// catalogue are v1's.
func TestIntegrationDetail(t *testing.T) {
	requireInstance(t)

	installed := harness.List[integration](t, v2, "/api/v2/integrations?name=s3")
	require.Len(t, installed.Items, 1)
	s3 := installed.Items[0]

	byName := harness.List[integration](t, v2, "/api/v2/integrations?name=s3&supports=store")
	require.Len(t, byName.Items, 1)
	require.Equal(t, s3.Version, byName.Items[0].Version)
	require.Empty(t, harness.List[integration](t, v2, "/api/v2/integrations?name=nope").Items)

	var byID integration
	v2.OK(t, http.MethodGet, "/api/v2/integrations/"+s3.ID, nil, &byID)
	require.Equal(t, "s3", byID.Name)
	require.True(t, byID.IsCurrent)
	require.Contains(t, byID.Supports, "store")
	v2.Refused(t, http.MethodGet, "/api/v2/integrations/"+missing, nil, harness.CodeNotFound)

	var current integration
	v2.OK(t, http.MethodGet, "/api/v2/integrations/resolve?name=s3&supports=store", nil, &current)
	require.Equal(t, s3.ID, current.ID, "s3 is current for a store")
	v2.Refused(t, http.MethodGet, "/api/v2/integrations/resolve?name=nope&supports=store", nil, harness.CodeNotFound)

	t.Run("protocols", func(t *testing.T) {
		gap(t, "read the form schema of an integration, or its protocols for a connector type (v1 GET /integrations/schema/{id}, /integrations/{id}?type=; pcpctl integration schema/describe)")
	})
	t.Run("install and make current", func(t *testing.T) {
		gap(t, "install a version from the catalogue and make it current (v1 POST /integrations/installed/current, pcpctl integration install --current)")
	})
}

// TestSecretProviderCreate: a vault is an instance of a secret-provider
// integration, and the rig installs none, so every creation is refused; the
// reads of one that does not exist are not-founds.
func TestSecretProviderCreate(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-vault")

	v2.Refused(t, http.MethodPost, "/api/v2/secret-providers", map[string]any{"type": "nope", "name": name, "protocol": "x"}, harness.CodeInvalidArgument)
	v2.Refused(t, http.MethodPost, "/api/v2/secret-providers", map[string]any{"name": name}, harness.CodeInvalidArgument)
	require.Empty(t, harness.List[any](t, v2, "/api/v2/secret-providers?search="+name).Items)

	t.Run("not a secret provider", func(t *testing.T) {
		// s3 is installed and is no secret provider: the type does not
		// resolve to one.
		v2.Refused(t, http.MethodPost, "/api/v2/secret-providers", map[string]any{"type": "s3", "name": name, "protocol": "s3"}, harness.CodeInvalidArgument)
	})
	t.Run("absent", func(t *testing.T) {
		gap(t, "read a vault's capabilities, secrets or form (v1 /secret_providers/{id}/capabilities, /secrets, /form; pcpctl secret-provider capabilities/secrets/form)")
	})
}

// TestSLATemplates creates a template with a temporality pinned to a store,
// patches it, binds it to a source, reads the contract back, unbinds it,
// deletes the template, and sees it gone.
func TestSLATemplates(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, map[string]string{"key": "port", "value": "1"}, map[string]string{"key": "use_tls", "value": "false"})
	var src connector
	v2.OK(t, http.MethodPost, "/api/v2/connectors", map[string]any{
		"type": "source", "integration": "s3", "name": harness.Unique("qa-source"), "protocol": "s3",
		"urn_id": r.URNID, "fields": s3Fields,
	}, &src)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/connectors/"+src.ID, nil) })

	envs := harness.List[taxon](t, v2, "/api/v2/environments?search=production")
	require.Len(t, envs.Items, 1)

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
	v2.OK(t, http.MethodPost, "/api/v2/sla-templates", map[string]any{
		"name": name, "environment_id": envs.Items[0].ID,
		"temporalities": map[string]any{"day": map[string]any{"frequency": 1, "retention": 7, "store_id": c.ID}},
	}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	require.NotEmpty(t, created.Status)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/sla-templates/"+created.ID, nil) })

	var got template
	v2.OK(t, http.MethodGet, "/api/v2/sla-templates/"+created.ID, nil, &got)
	require.Equal(t, "production", got.Environment, "the read names the environment")
	require.Contains(t, idsOf(t, harness.List[template](t, v2, "/api/v2/sla-templates?limit=200").Items), created.ID)

	v2.OK(t, http.MethodPatch, "/api/v2/sla-templates/"+created.ID, map[string]any{"tags": []string{"qa"}}, &got)
	require.Equal(t, []string{"qa"}, got.Tags)
	require.Greater(t, got.Revision, created.Revision, "a change is a new revision")

	type contract struct {
		ID         string `json:"id"`
		TemplateID string `json:"template_id"`
		SourceID   string `json:"source_id"`
	}
	var ct contract
	v2.OK(t, http.MethodPost, "/api/v2/sla-contracts", map[string]string{"template_id": created.ID, "source_id": src.ID}, &ct)
	require.Equal(t, created.ID, ct.TemplateID)
	require.Equal(t, src.ID, ct.SourceID)
	var read contract
	v2.OK(t, http.MethodGet, "/api/v2/sla-contracts/"+ct.ID, nil, &read)
	require.Equal(t, ct, read)
	require.Contains(t, idsOf(t, harness.List[contract](t, v2, "/api/v2/sla-contracts?limit=200&template_id="+created.ID).Items), ct.ID)

	t.Run("service levels", func(t *testing.T) {
		gap(t, "list the service levels and how well they are met (v1 GET /account/organizations/{id}/slas, pcpctl sla list)")
	})

	v2.OK(t, http.MethodDelete, "/api/v2/sla-contracts/"+ct.ID, nil, nil)
	v2.Refused(t, http.MethodGet, "/api/v2/sla-contracts/"+ct.ID, nil, harness.CodeNotFound)
	v2.OK(t, http.MethodDelete, "/api/v2/sla-templates/"+created.ID, nil, nil)
	v2.Refused(t, http.MethodGet, "/api/v2/sla-templates/"+created.ID, nil, harness.CodeNotFound)

	t.Run("refusals", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/sla-templates", map[string]any{}, harness.CodeInvalidArgument)
		v2.Refused(t, http.MethodPost, "/api/v2/sla-contracts", map[string]any{"template_id": missing, "source_id": src.ID}, harness.CodeInvalidArgument)
	})
}

// idsOf collects the ids of a listing's rows, whatever their type, through
// their JSON.
func idsOf[T any](t *testing.T, rows []T) []string {
	t.Helper()
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		var withID struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(raw, &withID))
		ids = append(ids, withID.ID)
	}
	return ids
}

// TestDeploymentExtras: the deployment describes itself, dumps its database,
// lists the webhook events, and enters and leaves safe mode; the debug log,
// the appliance and the network are v1's.
func TestDeploymentExtras(t *testing.T) {
	requireInstance(t)

	var system struct {
		Version  string `json:"version"`
		Enrolled bool   `json:"enrolled"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/system", nil, &system)
	require.NotEmpty(t, system.Version)
	require.True(t, system.Enrolled)

	events := harness.List[struct {
		Domain string   `json:"domain"`
		Types  []string `json:"types"`
	}](t, v2, "/api/v2/webhook-events?limit=200")
	require.NotEmpty(t, events.Items)

	t.Run("database dump", func(t *testing.T) {
		status, dump := v2.Do(t, http.MethodGet, "/api/v2/system/database-dump", nil)
		require.Equal(t, http.StatusOK, status)
		finding(t, "GET /system/database-dump answers 200 with an empty body in the rig, as v1 does: the daemon runs where no pg_dump is")
		require.Contains(t, string(dump), "CREATE TABLE")
	})

	t.Run("safe mode", func(t *testing.T) {
		var state struct {
			Enabled bool `json:"enabled"`
		}
		v2.OK(t, http.MethodPost, "/api/v2/system/actions/enter-safe-mode", map[string]string{"reason": "qa"}, &state)
		t.Cleanup(func() { v2.Do(t, http.MethodPost, "/api/v2/system/actions/exit-safe-mode", nil) })
		require.True(t, state.Enabled)
		var info struct {
			SafeMode bool `json:"safe_mode"`
		}
		v2.OK(t, http.MethodGet, "/api/v2/system", nil, &info)
		require.True(t, info.SafeMode)
		v2.OK(t, http.MethodPost, "/api/v2/system/actions/exit-safe-mode", nil, &state)
		require.False(t, state.Enabled)
	})
	t.Run("debug log", func(t *testing.T) {
		gap(t, "switch the deployment's debug logging on for a window (v1 PUT /debuglog, pcpctl deployment debug-log --enable)")
	})
	t.Run("update", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/system/actions/update", map[string]any{"next_version": "v0.0.0"}, harness.CodeInvalidArgument)
	})
	t.Run("logs", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/system/logs?date=yesterday", nil, harness.CodeInvalidArgument)
		// The rig's daemon writes no log file of its own.
		finding(t, "GET /system/logs answers 500 when there is no log file for the day; v1 answers 404")
		v2.Refused(t, http.MethodGet, "/api/v2/system/logs", nil, harness.CodeNotFound)
	})
	t.Run("appliance", func(t *testing.T) {
		gap(t, "read the appliance's hardware or configure a network interface (v1 GET /appliance/specs, POST /network/interfaces; pcpctl deployment specs, interfaces configure)")
	})
	t.Run("packages", func(t *testing.T) {
		// A package is uploaded as a multipart form; a body that is not one
		// is refused.
		v2.Refused(t, http.MethodPost, "/api/v2/integrations", map[string]any{"package": "not-a-file"}, harness.CodeInvalidArgument)
	})
}
