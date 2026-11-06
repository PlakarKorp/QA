// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type inventory struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IntegrationID string `json:"integration_id"`
	Residency     string `json:"residency"`
}

type resource struct {
	ID          string   `json:"id"`
	InventoryID string   `json:"inventory_id"`
	URNID       string   `json:"urn_id"`
	URN         string   `json:"urn"`
	Name        string   `json:"name"`
	Class       string   `json:"class"`
	Subclass    string   `json:"subclass"`
	Service     string   `json:"service"`
	Tags        []string `json:"tags"`
	Locked      bool     `json:"locked"`
	Coverage    string   `json:"coverage"`
	Endpoints   []struct {
		Kind     string `json:"kind"`
		Endpoint string `json:"endpoint"`
	} `json:"endpoints"`
}

func createInventory(t *testing.T, c *harness.V2, name string) inventory {
	t.Helper()
	var inv inventory
	c.OK(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "self-managed", "name": name}, &inv)
	t.Cleanup(func() { c.Do(t, http.MethodDelete, "/api/v2/inventories/"+inv.ID+"?force=true", nil) })
	return inv
}

// addResource adds a compute resource with a urn of its own, as in v1.
func addResource(t *testing.T, c *harness.V2, inv, name string, extra map[string]any) resource {
	t.Helper()
	body := map[string]any{"urn": "urn:qa:" + harness.Unique(name), "class": "compute", "name": name}
	for k, v := range extra {
		body[k] = v
	}
	var r resource
	c.OK(t, http.MethodPost, "/api/v2/inventories/"+inv+"/actions/add-resource", body, &r)
	return r
}

func inventoryIDs(t *testing.T, c *harness.V2) []string {
	t.Helper()
	page := harness.List[inventory](t, c, "/api/v2/inventories?limit=200")
	ids := make([]string, len(page.Items))
	for i, inv := range page.Items {
		ids[i] = inv.ID
	}
	return ids
}

func resourceIDs(t *testing.T, c *harness.V2, query string) []string {
	t.Helper()
	page := harness.List[resource](t, c, "/api/v2/resources?limit=200"+query)
	ids := make([]string, len(page.Items))
	for i, r := range page.Items {
		ids[i] = r.ID
	}
	return ids
}

// integrationName resolves the integration an inventory references.
func integrationName(t *testing.T, c *harness.V2, id string) string {
	t.Helper()
	var integration struct {
		Name string `json:"name"`
	}
	c.OK(t, http.MethodGet, "/api/v2/integrations/"+id, nil, &integration)
	return integration.Name
}

// TestInventoryLifecycle creates a self-managed inventory, reads it back,
// finds it in the listing (by id, and by name through the search filter),
// deletes it, and sees it gone.
func TestInventoryLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-inv")

	var created inventory
	v2.OK(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "self-managed", "name": name}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	require.Contains(t, integrationName(t, v2, created.IntegrationID), "self-managed", "the type is the integration it references")

	var byID inventory
	v2.OK(t, http.MethodGet, "/api/v2/inventories/"+created.ID, nil, &byID)
	require.Equal(t, created.ID, byID.ID)
	require.Equal(t, name, byID.Name)

	require.Contains(t, inventoryIDs(t, v2), created.ID)
	search := harness.List[inventory](t, v2, "/api/v2/inventories?search="+name)
	require.Len(t, search.Items, 1)
	require.Equal(t, created.ID, search.Items[0].ID, "search finds it by name")

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/inventories/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, status)

	v2.Refused(t, http.MethodGet, "/api/v2/inventories/"+created.ID, nil, harness.CodeNotFound)
	require.NotContains(t, inventoryIDs(t, v2), created.ID)
	require.Empty(t, harness.List[inventory](t, v2, "/api/v2/inventories?search="+name).Items)
}

// TestInventoryCreateMisuse: the creations that must be refused.
func TestInventoryCreateMisuse(t *testing.T) {
	requireInstance(t)
	before := len(inventoryIDs(t, v2))

	t.Run("no provider", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories", map[string]any{"name": harness.Unique("qa-inv")}, harness.CodeInvalidArgument)
	})
	t.Run("unknown provider", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "hetzner", "name": harness.Unique("qa-inv")}, harness.CodeInvalidArgument)
	})
	t.Run("no name", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "self-managed"}, harness.CodeInvalidArgument)
	})
	require.Len(t, inventoryIDs(t, v2), before)

	t.Run("cloud provider with no configuration", func(t *testing.T) {
		// Unlike v1, which refuses an aws inventory outright on this rig, v2
		// creates it, installing the aws integration from the catalogue on
		// the way, and leaves the credentials to be checked at sync time. The
		// package is uninstalled afterwards so the instance is left as found.
		var inv inventory
		v2.OK(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "aws", "name": harness.Unique("qa-aws")}, &inv)
		t.Cleanup(func() {
			v2.Do(t, http.MethodDelete, "/api/v2/inventories/"+inv.ID+"?force=true", nil)
			v2.Do(t, http.MethodDelete, "/api/v2/integrations/"+inv.IntegrationID, nil)
		})
		require.Equal(t, "aws", integrationName(t, v2, inv.IntegrationID))
	})
}

// TestInventoryDeleteMisuse: deleting nothing, and deleting twice.
func TestInventoryDeleteMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("absent", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/inventories/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("malformed", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/inventories/nope", nil, harness.CodeInvalidArgument)
	})
	t.Run("twice", func(t *testing.T) {
		inv := createInventory(t, v2, harness.Unique("qa-inv"))
		v2.OK(t, http.MethodDelete, "/api/v2/inventories/"+inv.ID, nil, nil)
		v2.Refused(t, http.MethodDelete, "/api/v2/inventories/"+inv.ID, nil, harness.CodeNotFound)
	})
}

// TestInventoryDuplicateName: no uniqueness check, so two inventories may
// share a name and the search shows both.
func TestInventoryDuplicateName(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-twin-inv")

	first := createInventory(t, v2, name)
	second := createInventory(t, v2, name)
	require.NotEqual(t, first.ID, second.ID)

	var twins []string
	for _, inv := range harness.List[inventory](t, v2, "/api/v2/inventories?search="+name).Items {
		twins = append(twins, inv.ID)
	}
	require.ElementsMatch(t, []string{first.ID, second.ID}, twins)
}

// TestInventoryIsolation: an inventory is its organization's. A badge
// scoped to a sub-organization neither lists nor reads it, and cannot delete
// it: a foreign resource is a not-found, never a hint that it exists.
func TestInventoryIsolation(t *testing.T) {
	requireInstance(t)

	inv := createInventory(t, v2, harness.Unique("qa-inv"))
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	in := scoped(t, v2, child.ID)

	require.NotContains(t, inventoryIDs(t, in), inv.ID)
	in.Refused(t, http.MethodGet, "/api/v2/inventories/"+inv.ID, nil, harness.CodeNotFound)
	in.Refused(t, http.MethodDelete, "/api/v2/inventories/"+inv.ID, nil, harness.CodeNotFound)
	v2.OK(t, http.MethodGet, "/api/v2/inventories/"+inv.ID, nil, nil)
}

// TestResourceLifecycle fills a self-managed inventory by hand: add a
// resource, read it, see it in the inventory's resources and in the listing
// of every resource, count it in the coverage report, remove it, and see it
// gone.
func TestResourceLifecycle(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))

	// A subclass is checked against the class tree, which v1 does not do, so
	// the resource is filed under a class that has one.
	r := addResource(t, v2, inv.ID, "laptop", map[string]any{"class": "database", "subclass": "postgresql", "service": "rds", "tags": []string{"qa", "lab"}})
	require.NotEmpty(t, r.ID)
	require.NotEmpty(t, r.URNID)
	require.Equal(t, inv.ID, r.InventoryID)
	require.Equal(t, "laptop", r.Name)
	require.Equal(t, "database", r.Class)
	require.Equal(t, "postgresql", r.Subclass)
	require.Equal(t, "rds", r.Service)
	require.ElementsMatch(t, []string{"qa", "lab"}, r.Tags)

	var got resource
	v2.OK(t, http.MethodGet, "/api/v2/resources/"+r.ID, nil, &got)
	require.Equal(t, r.ID, got.ID)
	require.Equal(t, "laptop", got.Name)

	require.Equal(t, []string{r.ID}, resourceIDs(t, v2, "&inventory_id="+inv.ID))
	require.Contains(t, resourceIDs(t, v2, ""), r.ID)

	require.Eventually(t, func() bool { return inventoryCoverage(t, inv.ID).Total == 1 },
		30*time.Second, 2*time.Second, "the coverage report counts what the inventory holds")

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/resources/"+r.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	v2.Refused(t, http.MethodGet, "/api/v2/resources/"+r.ID, nil, harness.CodeNotFound)
	require.Empty(t, resourceIDs(t, v2, "&inventory_id="+inv.ID))
}

// TestResourceMisuse: adding with nothing to name the resource, into an
// inventory that does not exist, under a urn already taken, and the removals
// that must be refused.
func TestResourceMisuse(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))

	t.Run("nothing to name it", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories/"+inv.ID+"/actions/add-resource",
			map[string]any{"urn": "urn:qa:nameless", "class": "compute"}, harness.CodeInvalidArgument)
	})
	t.Run("no urn", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories/"+inv.ID+"/actions/add-resource",
			map[string]any{"name": "ghost", "class": "compute"}, harness.CodeInvalidArgument)
	})
	t.Run("absent inventory", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories/"+missing+"/actions/add-resource",
			map[string]any{"urn": "urn:qa:x", "name": "ghost", "class": "compute"}, harness.CodeNotFound)
	})
	t.Run("entry that is not JSON", func(t *testing.T) {
		status, _ := v2.Do(t, http.MethodPost, "/api/v2/inventories/"+inv.ID+"/actions/add-resource", "{nope")
		require.Equal(t, http.StatusBadRequest, status)
	})
	t.Run("duplicate urn", func(t *testing.T) {
		// The urn is the identity: adding under a urn already there replaces
		// that resource rather than conflicting as v1 does, and the urn id
		// stays.
		r := addResource(t, v2, inv.ID, "first", nil)
		var again resource
		v2.OK(t, http.MethodPost, "/api/v2/inventories/"+inv.ID+"/actions/add-resource",
			map[string]any{"urn": r.URN, "name": "second", "class": "compute"}, &again)
		require.Equal(t, r.URNID, again.URNID)
		listed := harness.List[resource](t, v2, "/api/v2/resources?inventory_id="+inv.ID)
		require.Len(t, listed.Items, 1)
		require.Equal(t, "second", listed.Items[0].Name)
		v2.OK(t, http.MethodDelete, "/api/v2/resources/"+again.ID, nil, nil)
	})
	t.Run("subclass not in the class tree", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/inventories/"+inv.ID+"/actions/add-resource",
			map[string]any{"urn": "urn:qa:vm", "name": "vm", "class": "compute", "subclass": "vm"}, harness.CodeInvalidArgument)
	})
	t.Run("get absent", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/resources/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("delete absent", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/resources/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("delete twice", func(t *testing.T) {
		r := addResource(t, v2, inv.ID, "gone", nil)
		v2.OK(t, http.MethodDelete, "/api/v2/resources/"+r.ID, nil, nil)
		v2.Refused(t, http.MethodDelete, "/api/v2/resources/"+r.ID, nil, harness.CodeNotFound)
	})
}

// TestResourceUpdate renames a resource and replaces its tags with a patch;
// the urn and the id survive, and a patch of nothing is a not-found.
func TestResourceUpdate(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))
	r := addResource(t, v2, inv.ID, "before", map[string]any{"tags": []string{"old"}})

	// An edit re-generates the resource row: the id in the answer is the one
	// to read afterwards, and the urn id is what stays.
	var updated resource
	v2.OK(t, http.MethodPatch, "/api/v2/resources/"+r.ID, map[string]any{"name": "after", "tags": []string{"new"}}, &updated)
	require.Equal(t, r.URNID, updated.URNID)
	require.Equal(t, "after", updated.Name)
	require.Equal(t, []string{"new"}, updated.Tags)

	var got resource
	v2.OK(t, http.MethodGet, "/api/v2/resources/"+updated.ID, nil, &got)
	require.Equal(t, "after", got.Name)
	require.Equal(t, []string{"new"}, got.Tags)
	require.Equal(t, []string{updated.ID}, resourceIDs(t, v2, "&urn_id="+r.URNID), "the urn id finds the current row")

	v2.Refused(t, http.MethodPatch, "/api/v2/resources/"+missing, map[string]any{"name": "ghost"}, harness.CodeNotFound)
	v2.Refused(t, http.MethodPatch, "/api/v2/resources/"+updated.ID, map[string]any{"name": ""}, harness.CodeInvalidArgument)
}

// TestResourceLock takes and releases the lock on a resource, reading the
// state back each time; locking nothing is a not-found.
func TestResourceLock(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))
	r := addResource(t, v2, inv.ID, "lockable", nil)
	require.False(t, r.Locked)

	var locked resource
	v2.OK(t, http.MethodPost, "/api/v2/resources/"+r.ID+"/actions/lock", nil, &locked)
	require.True(t, locked.Locked)
	var got resource
	v2.OK(t, http.MethodGet, "/api/v2/resources/"+r.ID, nil, &got)
	require.True(t, got.Locked)

	v2.OK(t, http.MethodPost, "/api/v2/resources/"+r.ID+"/actions/unlock", nil, &locked)
	require.False(t, locked.Locked)
	v2.OK(t, http.MethodGet, "/api/v2/resources/"+r.ID, nil, &got)
	require.False(t, got.Locked)

	v2.Refused(t, http.MethodPost, "/api/v2/resources/"+missing+"/actions/lock", nil, harness.CodeNotFound)
}

// TestInventoryResidency pins where an inventory's data may live, refuses a
// place that is not a country, and clears it. v1 also lets one resource
// override its inventory's residency; v2 keeps residency on the inventory.
func TestInventoryResidency(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))

	var got inventory
	v2.OK(t, http.MethodPatch, "/api/v2/inventories/"+inv.ID, map[string]any{"residency": "FR"}, &got)
	require.Equal(t, "FR", got.Residency)
	v2.OK(t, http.MethodGet, "/api/v2/inventories/"+inv.ID, nil, &got)
	require.Equal(t, "FR", got.Residency)

	v2.OK(t, http.MethodPatch, "/api/v2/inventories/"+inv.ID, map[string]any{"residency": "FRA"}, &got)
	require.Equal(t, "FR", got.Residency, "any ISO 3166-1 spelling names the same country")

	v2.Refused(t, http.MethodPatch, "/api/v2/inventories/"+inv.ID, map[string]any{"residency": "Narnia"}, harness.CodeInvalidArgument)
	v2.OK(t, http.MethodGet, "/api/v2/inventories/"+inv.ID, nil, &got)
	require.Equal(t, "FR", got.Residency, "a refused residency changes nothing")

	v2.OK(t, http.MethodPatch, "/api/v2/inventories/"+inv.ID, map[string]any{"residency": ""}, &got)
	require.Empty(t, got.Residency, "an empty residency clears it")

	v2.Refused(t, http.MethodPatch, "/api/v2/inventories/"+missing, map[string]any{"residency": "FR"}, harness.CodeNotFound)

	t.Run("resource override", func(t *testing.T) {
		gap(t, "pin a residency on one resource (v1 PATCH /inventories/{id}/resources/{urn}/residency, pcpctl inventory resources residency)")
	})
}

type coverage struct {
	Total       int `json:"total"`
	Protected   int `json:"protected"`
	Unprotected int `json:"unprotected"`
	Excluded    int `json:"excluded"`
	Inventories []struct {
		InventoryID string `json:"inventory_id"`
		Total       int    `json:"total"`
		Unprotected int    `json:"unprotected"`
	} `json:"inventories"`
}

func inventoryCoverage(t *testing.T, inventoryID string) coverage {
	t.Helper()
	var report coverage
	v2.OK(t, http.MethodGet, "/api/v2/reports/inventory-coverage", nil, &report)
	for _, inv := range report.Inventories {
		if inv.InventoryID == inventoryID {
			return coverage{Total: inv.Total, Unprotected: inv.Unprotected}
		}
	}
	return coverage{}
}

// TestInventoryClassesAndCoverage: the class tree a resource may be filed
// under is reference data, and the coverage report sees a resource with
// nothing protecting it as unprotected.
func TestInventoryClassesAndCoverage(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))

	classes := harness.List[struct {
		Class      string   `json:"class"`
		Subclasses []string `json:"subclasses"`
	}](t, v2, "/api/v2/resource-classes")
	byName := map[string][]string{}
	for _, c := range classes.Items {
		byName[c.Class] = c.Subclasses
	}
	require.Contains(t, byName, "compute")
	require.Contains(t, byName["object-storage"], "s3")

	var before coverage
	v2.OK(t, http.MethodGet, "/api/v2/reports/inventory-coverage", nil, &before)

	r := addResource(t, v2, inv.ID, "bare", nil)
	var got resource
	v2.OK(t, http.MethodGet, "/api/v2/resources/"+r.ID, nil, &got)
	require.Equal(t, "unprotected", got.Coverage, "the resource says what covers it")

	require.Eventually(t, func() bool {
		var after coverage
		v2.OK(t, http.MethodGet, "/api/v2/reports/inventory-coverage", nil, &after)
		return after.Unprotected == before.Unprotected+1 && after.Protected == before.Protected
	}, 30*time.Second, 2*time.Second, "an unprotected resource counts as such")
}

// TestInventoryExtras: an inventory is patched, takes resources from a CSV
// import, and a resource is left out of the coverage count and counted
// again, and carries its addresses; a cloud provider is created with no
// configuration, installing its integration on the way. The sync, its
// period, the secrets and the capabilities are v1's.
func TestInventoryExtras(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, v2, harness.Unique("qa-inv"))
	r := addResource(t, v2, inv.ID, "addressed", map[string]any{"endpoints": []map[string]string{{"endpoint": "127.0.0.1"}}})

	t.Run("sync", func(t *testing.T) {
		gap(t, "re-read an inventory from its provider on demand, or set how often it is (v1 GET /inventories/{id}/sync, PATCH .../sync-period; pcpctl inventory sync, sync-period)")
	})
	t.Run("secrets and capabilities", func(t *testing.T) {
		gap(t, "reveal an inventory's secrets, or say what the caller may do to it (v1 GET /inventories/{id}/secrets, /capabilities; pcpctl inventory secrets, capabilities)")
	})
	t.Run("update", func(t *testing.T) {
		// Its own inventory: the patch empties the one it renames.
		own := createInventory(t, v2, harness.Unique("qa-inv"))
		kept := addResource(t, v2, own.ID, "kept", nil)
		var updated inventory
		v2.OK(t, http.MethodPatch, "/api/v2/inventories/"+own.ID, map[string]any{"name": own.Name + "-renamed"}, &updated)
		require.Equal(t, own.ID, updated.ID)
		require.Equal(t, own.Name+"-renamed", updated.Name)
		var got inventory
		v2.OK(t, http.MethodGet, "/api/v2/inventories/"+own.ID, nil, &got)
		require.Equal(t, own.Name+"-renamed", got.Name)
		v2.Refused(t, http.MethodPatch, "/api/v2/inventories/"+own.ID, map[string]any{"name": ""}, harness.CodeInvalidArgument)
		v2.Refused(t, http.MethodPatch, "/api/v2/inventories/"+missing, map[string]any{"name": "ghost"}, harness.CodeNotFound)
		t.Run("resources", func(t *testing.T) {
			finding(t, "PATCH /inventories/{id} (a rename) empties the inventory's resources; v1's reconfiguration keeps them")
			require.Contains(t, resourceIDs(t, v2, "&inventory_id="+own.ID), kept.ID, "a patch keeps the resources")
		})
	})
	t.Run("endpoints", func(t *testing.T) {
		var got resource
		v2.OK(t, http.MethodGet, "/api/v2/resources/"+r.ID, nil, &got)
		require.Len(t, got.Endpoints, 1)
		require.Equal(t, "127.0.0.1", got.Endpoints[0].Endpoint)
	})
	t.Run("coverage exclusion", func(t *testing.T) {
		var before struct {
			Excluded int `json:"excluded"`
		}
		v2.OK(t, http.MethodGet, "/api/v2/reports/inventory-coverage", nil, &before)
		var got resource
		v2.OK(t, http.MethodPatch, "/api/v2/resources/"+r.ID, map[string]any{"excluded_from_coverage": true}, &got)
		require.Equal(t, "excluded", got.Coverage)
		var after struct {
			Excluded int `json:"excluded"`
		}
		v2.OK(t, http.MethodGet, "/api/v2/reports/inventory-coverage", nil, &after)
		require.Equal(t, before.Excluded+1, after.Excluded)
		v2.OK(t, http.MethodPatch, "/api/v2/resources/"+r.ID, map[string]any{"excluded_from_coverage": false}, &got)
		require.NotEqual(t, "excluded", got.Coverage)
		v2.Refused(t, http.MethodPatch, "/api/v2/resources/"+missing, map[string]any{"excluded_from_coverage": true}, harness.CodeNotFound)
	})
	t.Run("import", func(t *testing.T) {
		bulk := createInventory(t, v2, harness.Unique("qa-bulk"))
		urn := "urn:qa:" + harness.Unique("imported")
		status, raw := v2.Do(t, http.MethodPost, "/api/v2/inventories/"+bulk.ID+"/actions/import-resources", nil)
		require.Equal(t, http.StatusBadRequest, status, "an import with nothing to import: %s", raw)

		req, err := http.NewRequest(http.MethodPost, v2.Base+"/api/v2/inventories/"+bulk.ID+"/actions/import-resources",
			strings.NewReader("compute;imported;"+urn+";10.0.0.1\n"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "text/csv")
		req.Header.Set("Authorization", "Bearer "+v2.Token)
		resp, err := v2.HTTP.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s", body)
		var imported struct {
			Total int        `json:"total"`
			Items []resource `json:"items"`
		}
		require.NoError(t, json.Unmarshal(body, &imported))
		require.Len(t, imported.Items, 1)
		require.Equal(t, "imported", imported.Items[0].Name)
		require.Len(t, resourceIDs(t, v2, "&inventory_id="+bulk.ID), 1, "the import is the inventory's whole set")
	})
	t.Run("generic creation", func(t *testing.T) {
		// One route takes every provider as a type; a provider whose
		// integration is in the store is installed on the way, and one the
		// store does not know is refused.
		name := harness.Unique("qa-generic")
		var created inventory
		v2.OK(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "self-managed", "name": name}, &created)
		t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/inventories/"+created.ID+"?force=true", nil) })
		require.Equal(t, name, created.Name)
		require.Contains(t, integrationName(t, v2, created.IntegrationID), "self-managed")
		v2.Refused(t, http.MethodPost, "/api/v2/inventories", map[string]any{"type": "hetzner", "name": harness.Unique("qa-inv")}, harness.CodeInvalidArgument)
		v2.Refused(t, http.MethodPost, "/api/v2/inventories", map[string]any{"name": harness.Unique("qa-inv")}, harness.CodeInvalidArgument)
	})
}
