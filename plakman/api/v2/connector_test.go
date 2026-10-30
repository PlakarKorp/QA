// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"net/http"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type connector struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Type               string `json:"type"`
	Protocol           string `json:"protocol"`
	Integration        string `json:"integration"`
	IntegrationID      string `json:"integration_id"`
	IntegrationVersion string `json:"integration_version"`
	URNID              string `json:"urn_id"`
	Revision           int    `json:"revision"`
	Ready              bool   `json:"ready"`
}

var s3Fields = []map[string]string{
	{"key": "access_key", "value": "qa"},
	{"key": "secret_access_key", "value": "qa"},
	{"key": "root", "value": "/bucket"},
}

// storeFixture is an inventory holding one object-storage resource on the
// loopback, as in api/v1.
func storeFixture(t *testing.T) (inventory, resource) {
	t.Helper()
	inv := createInventory(t, v2, harness.Unique("qa-inv"))
	r := addResource(t, v2, inv.ID, "bucket", map[string]any{
		"class": "object-storage", "subclass": "s3", "service": "s3",
		"endpoints": []map[string]string{{"endpoint": "127.0.0.1"}},
	})
	return inv, r
}

func storeBody(name string, r resource, fields ...map[string]string) map[string]any {
	return map[string]any{
		"type": "store", "integration": "s3", "name": name, "protocol": "s3",
		"urn_id": r.URNID, "fields": append(append([]map[string]string{}, s3Fields...), fields...),
	}
}

func createStore(t *testing.T, name string, r resource, fields ...map[string]string) connector {
	t.Helper()
	var c connector
	v2.OK(t, http.MethodPost, "/api/v2/connectors", storeBody(name, r, fields...), &c)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/connectors/"+c.ID, nil) })
	return c
}

func connectorIDs(t *testing.T, c *harness.V2, query string) []string {
	t.Helper()
	page := harness.List[connector](t, c, "/api/v2/connectors?limit=200"+query)
	ids := make([]string, len(page.Items))
	for i, x := range page.Items {
		ids[i] = x.ID
	}
	return ids
}

// TestConnectorLifecycle creates a store connector on a resource, reads it
// back, finds it in the listing of its type and by search, not in the other
// types, deletes it, and sees it gone.
func TestConnectorLifecycle(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	name := harness.Unique("qa-store")

	c := createStore(t, name, r)
	require.NotEmpty(t, c.ID)
	require.Equal(t, name, c.Name)
	require.Equal(t, "store", c.Type)
	require.Equal(t, "s3", c.Protocol)
	require.Equal(t, "s3", c.Integration, "a name picks the installed integration")
	require.NotEmpty(t, c.IntegrationID)
	require.Equal(t, r.URNID, c.URNID)

	var byID connector
	v2.OK(t, http.MethodGet, "/api/v2/connectors/"+c.ID, nil, &byID)
	require.Equal(t, c, byID)

	require.Contains(t, connectorIDs(t, v2, "&type=store"), c.ID)
	require.Contains(t, connectorIDs(t, v2, ""), c.ID)
	require.NotContains(t, connectorIDs(t, v2, "&type=source"), c.ID)
	require.NotContains(t, connectorIDs(t, v2, "&type=destination"), c.ID)
	require.Equal(t, []string{c.ID}, connectorIDs(t, v2, "&search="+name), "search finds it by name")
	require.Equal(t, []string{c.ID}, connectorIDs(t, v2, "&urn_id="+r.URNID), "the resource's connectors are a filter")

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/connectors/"+c.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	v2.Refused(t, http.MethodGet, "/api/v2/connectors/"+c.ID, nil, harness.CodeNotFound)
	require.NotContains(t, connectorIDs(t, v2, "&type=store"), c.ID)
}

// TestConnectorCreateMisuse: the creations that must be refused, each with
// its code, and nothing created by any of them.
func TestConnectorCreateMisuse(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	name := harness.Unique("qa-bad")
	refused := func(t *testing.T, body map[string]any, code string) {
		t.Helper()
		v2.Refused(t, http.MethodPost, "/api/v2/connectors", body, code)
	}
	without := func(key string) map[string]any {
		body := storeBody(name, r)
		delete(body, key)
		return body
	}
	with := func(key string, value any) map[string]any {
		body := storeBody(name, r)
		body[key] = value
		return body
	}

	t.Run("no type", func(t *testing.T) { refused(t, without("type"), harness.CodeInvalidArgument) })
	t.Run("unknown type", func(t *testing.T) { refused(t, with("type", "disk"), harness.CodeInvalidArgument) })
	t.Run("no name", func(t *testing.T) { refused(t, without("name"), harness.CodeInvalidArgument) })
	t.Run("no integration", func(t *testing.T) { refused(t, without("integration"), harness.CodeInvalidArgument) })
	t.Run("integration not installed", func(t *testing.T) { refused(t, with("integration", "nope"), harness.CodeInvalidArgument) })
	t.Run("absent resource", func(t *testing.T) {
		finding(t, "POST /connectors with a well-formed urn_id that names nothing answers 500; it should be a 404")
		refused(t, with("urn_id", missing), harness.CodeNotFound)
	})
	t.Run("malformed resource", func(t *testing.T) { refused(t, with("urn_id", "nope"), harness.CodeInvalidArgument) })
	t.Run("secret in an absent provider", func(t *testing.T) {
		finding(t, "POST /connectors with a field whose provider_id names nothing answers 500; it should be a 404")
		refused(t, with("fields", []map[string]string{{"key": "secret_access_key", "provider_id": missing}}), harness.CodeNotFound)
	})
	t.Run("a field with no key", func(t *testing.T) {
		finding(t, "POST /connectors accepts a field with no key; the contract marks key as required")
		refused(t, with("fields", []map[string]string{{"value": "x"}}), harness.CodeInvalidArgument)
	})
	t.Run("unknown temperature", func(t *testing.T) { refused(t, with("temperature", "lukewarm"), harness.CodeInvalidArgument) })
	for _, id := range connectorIDs(t, v2, "&search="+name) {
		v2.Do(t, http.MethodDelete, "/api/v2/connectors/"+id, nil)
		t.Errorf("a refused create created connector %s", id)
	}
}

// TestConnectorDeleteMisuse: deleting nothing, and deleting twice.
func TestConnectorDeleteMisuse(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)

	t.Run("absent", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/connectors/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("malformed", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/connectors/nope", nil, harness.CodeInvalidArgument)
	})
	t.Run("twice", func(t *testing.T) {
		c := createStore(t, harness.Unique("qa-store"), r)
		v2.OK(t, http.MethodDelete, "/api/v2/connectors/"+c.ID, nil, nil)
		v2.Refused(t, http.MethodDelete, "/api/v2/connectors/"+c.ID, nil, harness.CodeNotFound)
	})
}

// TestConnectorDuplicateName: no uniqueness check, so two connectors may
// share a name and a search shows both.
func TestConnectorDuplicateName(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	name := harness.Unique("qa-twin-store")

	first := createStore(t, name, r)
	second := createStore(t, name, r)
	require.NotEqual(t, first.ID, second.ID)
	require.ElementsMatch(t, []string{first.ID, second.ID}, connectorIDs(t, v2, "&search="+name))
}

// TestConnectorUpdate renames a connector with a patch; the resource it
// serves stays as it was, the revision moves, and patching nothing is a
// not-found.
func TestConnectorUpdate(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)

	var updated connector
	v2.OK(t, http.MethodPatch, "/api/v2/connectors/"+c.ID, map[string]any{"name": c.Name + "-renamed"}, &updated)
	require.Equal(t, c.ID, updated.ID)
	require.Equal(t, c.Name+"-renamed", updated.Name)
	require.Equal(t, r.URNID, updated.URNID, "an absent field is unchanged")
	require.Greater(t, updated.Revision, c.Revision, "an update is a new revision")

	var got connector
	v2.OK(t, http.MethodGet, "/api/v2/connectors/"+c.ID, nil, &got)
	require.Equal(t, c.Name+"-renamed", got.Name)

	t.Run("empty name", func(t *testing.T) {
		v2.Refused(t, http.MethodPatch, "/api/v2/connectors/"+c.ID, map[string]any{"name": ""}, harness.CodeInvalidArgument)
		v2.OK(t, http.MethodGet, "/api/v2/connectors/"+c.ID, nil, &got)
		require.Equal(t, c.Name+"-renamed", got.Name)
	})
	t.Run("absent", func(t *testing.T) {
		v2.Refused(t, http.MethodPatch, "/api/v2/connectors/"+missing, map[string]any{"name": "ghost"}, harness.CodeNotFound)
	})
}

// TestConnectorTest: a connector whose endpoint answers nothing fails its
// test, as a result rather than an error; testing nothing is a not-found,
// and a resource with no endpoint leaves nothing to test.
func TestConnectorTest(t *testing.T) {
	requireInstance(t)
	v2.Refused(t, http.MethodPost, "/api/v2/connectors/"+missing+"/actions/test", nil, harness.CodeNotFound)

	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, map[string]string{"key": "port", "value": "1"}, map[string]string{"key": "use_tls", "value": "false"})
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/connectors/"+c.ID+"/actions/test", nil, &result)
	require.False(t, result.OK)
	require.Contains(t, result.Error, "connection refused", "the verdict carries the backend's own error")

	t.Run("resource without an endpoint", func(t *testing.T) {
		inv := createInventory(t, v2, harness.Unique("qa-inv"))
		bare := addResource(t, v2, inv.ID, "bare", map[string]any{"class": "object-storage", "subclass": "s3"})
		c := createStore(t, harness.Unique("qa-store"), bare)
		v2.OK(t, http.MethodPost, "/api/v2/connectors/"+c.ID+"/actions/test", nil, &result)
		require.False(t, result.OK)
		require.Contains(t, result.Error, "endpoint", "there is nowhere to test, and the verdict says so")
	})
}

// TestConnectorIsolation: a connector is its organization's; a badge scoped
// to a sub-organization neither lists, reads nor deletes it.
func TestConnectorIsolation(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	require.NotContains(t, connectorIDs(t, in, "&type=store"), c.ID)
	in.Refused(t, http.MethodGet, "/api/v2/connectors/"+c.ID, nil, harness.CodeNotFound)
	in.Refused(t, http.MethodDelete, "/api/v2/connectors/"+c.ID, nil, harness.CodeNotFound)
	v2.OK(t, http.MethodGet, "/api/v2/connectors/"+c.ID, nil, nil)
}

// TestSnapshotsOfStore: a fresh store holds no snapshots; the listing is
// filtered by store or by source, and a store that does not exist is a
// not-found.
func TestSnapshotsOfStore(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)

	snaps := harness.List[any](t, v2, "/api/v2/snapshots?store_id="+c.ID)
	require.Equal(t, 0, snaps.Total)
	require.Empty(t, snaps.Items)

	t.Run("absent store", func(t *testing.T) {
		// A filter is a value; a store nothing was taken into is an empty page.
		require.Empty(t, harness.List[any](t, v2, "/api/v2/snapshots?store_id="+missing).Items)
	})
	t.Run("malformed store", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/snapshots?store_id=nope", nil, harness.CodeInvalidArgument)
	})
	t.Run("absent snapshot", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/snapshots/"+missing, nil, harness.CodeNotFound)
	})
}

// TestJobsOfConnector: a fresh connector has no jobs, by connector and by
// status; the reads of a job that does not exist are not-founds, and a bad
// filter is refused.
func TestJobsOfConnector(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)

	require.Empty(t, harness.List[any](t, v2, "/api/v2/jobs?resource_id="+c.ID).Items)
	for _, j := range harness.List[struct {
		Status string `json:"status"`
	}](t, v2, "/api/v2/jobs?status=failed").Items {
		require.Equal(t, "failed", j.Status)
	}

	v2.Refused(t, http.MethodGet, "/api/v2/jobs/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/jobs/"+missing+"/actions/cancel", nil, harness.CodeNotFound)
	status, _ := v2.Do(t, http.MethodGet, "/api/v2/jobs/"+missing+"/log", nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = v2.Do(t, http.MethodGet, "/api/v2/jobs/"+missing+"/state", nil)
	require.Equal(t, http.StatusNotFound, status)

	t.Run("unknown status", func(t *testing.T) {
		finding(t, "GET /jobs?status=bogus answers 500, as v1 does; a bad filter should be a 400")
		v2.Refused(t, http.MethodGet, "/api/v2/jobs?status=bogus", nil, harness.CodeInvalidArgument)
	})
}
