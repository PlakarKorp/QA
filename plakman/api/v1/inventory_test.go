// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv1

import (
	"testing"
	"time"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type inventory struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Count     int    `json:"count"`
	Residency string `json:"residency"`
}

type resource struct {
	ID            string   `json:"id"`
	InventoryID   string   `json:"inventory_id"`
	InventoryName string   `json:"inventory_name"`
	URNID         string   `json:"urn_id"`
	URN           string   `json:"urn"`
	Name          string   `json:"name"`
	Class         string   `json:"class"`
	Subclass      string   `json:"subclass"`
	Service       string   `json:"service"`
	Tags          []string `json:"tags"`
	Residency     string   `json:"residency"`
	Locked        bool     `json:"locked"`
}

type page[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

// createInventory creates a self-managed inventory, the one kind that needs
// no provider credentials, and removes it when the test ends.
func createInventory(t *testing.T, p *harness.Pcpctl, name string) inventory {
	t.Helper()
	var inv inventory
	p.JSON(t, &inv, "inventory", "create", name, "--provider", "self-managed")
	t.Cleanup(func() { p.Run(t, "inventory", "delete", inv.ID, "--yes") })
	return inv
}

// addResource adds a compute resource by hand. The urn is the resource's
// identity within its inventory, so each one gets its own; without one every
// resource would share the empty urn and the second add would conflict.
func addResource(t *testing.T, p *harness.Pcpctl, inv, name string, extra ...string) resource {
	t.Helper()
	var r resource
	p.JSON(t, &r, append([]string{"inventory", "resources", "add", inv, name,
		"--class", "compute", "--urn", "urn:qa:" + harness.Unique(name)}, extra...)...)
	return r
}

// inventoryRow finds an inventory in the listing, which is where the resource
// count lives.
func inventoryRow(t *testing.T, p *harness.Pcpctl, id string) inventory {
	t.Helper()
	var invs page[inventory]
	p.JSON(t, &invs, "inventory", "list")
	for _, inv := range invs.Items {
		if inv.ID == id {
			return inv
		}
	}
	t.Fatalf("inventory %s is not listed", id)
	return inventory{}
}

func inventoryIDs(t *testing.T, p *harness.Pcpctl) []string {
	t.Helper()
	var invs page[inventory]
	p.JSON(t, &invs, "inventory", "list")
	ids := make([]string, len(invs.Items))
	for i, inv := range invs.Items {
		ids[i] = inv.ID
	}
	return ids
}

func resourceURNIDs(t *testing.T, p *harness.Pcpctl, args ...string) []string {
	t.Helper()
	var rs page[resource]
	p.JSON(t, &rs, append([]string{"inventory", "resources", "list"}, args...)...)
	ids := make([]string, len(rs.Items))
	for i, r := range rs.Items {
		ids[i] = r.URNID
	}
	return ids
}

// TestInventoryLifecycle creates a self-managed inventory, reads it back by
// id and by name, finds it in the listing, deletes it, and checks it is gone.
func TestInventoryLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-inv")

	var created inventory
	pcpctl.JSON(t, &created, "inventory", "create", name, "--provider", "self-managed")
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)

	var byID inventory
	pcpctl.JSON(t, &byID, "inventory", "get", created.ID)
	require.Equal(t, created.ID, byID.ID)
	require.Equal(t, name, byID.Name)
	require.Equal(t, "self-managed", byID.Type)
	require.Equal(t, "self-managed", inventoryRow(t, pcpctl, created.ID).Type)

	var byName inventory
	pcpctl.JSON(t, &byName, "inventory", "get", name)
	require.Equal(t, created.ID, byName.ID, "get resolves a name")

	require.Contains(t, inventoryIDs(t, pcpctl), created.ID)

	out := pcpctl.OK(t, "inventory", "delete", created.ID, "--yes")
	require.Contains(t, out, created.ID)

	pcpctl.Fails(t, harness.ExitNotFound, "inventory", "get", created.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "inventory", "get", name)
	require.NotContains(t, inventoryIDs(t, pcpctl), created.ID)
}

// TestInventoryCreateMisuse: the creations pcpctl or the API must refuse.
func TestInventoryCreateMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("no provider", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "inventory", "create", harness.Unique("qa-inv"))
	})
	t.Run("unknown provider", func(t *testing.T) {
		name := harness.Unique("qa-inv")
		pcpctl.Fails(t, harness.ExitError, "inventory", "create", name, "--provider", "hetzner")
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "get", name)
	})
	t.Run("configuration that is not JSON", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "inventory", "create", harness.Unique("qa-inv"),
			"--provider", "self-managed", "--config", "{not json")
	})
	t.Run("provider whose integration is not installed", func(t *testing.T) {
		// The rig installs s3 and nothing else, so a cloud inventory names an
		// integration the instance does not have: a not-found, and nothing
		// created.
		name := harness.Unique("qa-inv")
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "create", name, "--provider", "aws", "--config", "{}")
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "get", name)
	})
}

// TestInventoryDeleteMisuse: deleting nothing, deleting twice, and deleting
// without --yes.
func TestInventoryDeleteMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "delete", missing, "--yes")
	})
	t.Run("twice", func(t *testing.T) {
		inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
		pcpctl.OK(t, "inventory", "delete", inv.ID, "--yes")
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "delete", inv.ID, "--yes")
	})
	t.Run("without --yes", func(t *testing.T) {
		inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
		res := pcpctl.Fails(t, harness.ExitError, "inventory", "delete", inv.ID)
		require.Contains(t, res.Stderr, inv.Name)
		pcpctl.OK(t, "inventory", "get", inv.ID)
	})
}

// TestInventoryDuplicateName: inventories have no uniqueness check, so two
// may share a name; the name then stops resolving until one goes.
func TestInventoryDuplicateName(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-twin-inv")

	first := createInventory(t, pcpctl, name)
	second := createInventory(t, pcpctl, name)
	require.NotEqual(t, first.ID, second.ID)

	pcpctl.Fails(t, harness.ExitError, "inventory", "get", name)

	pcpctl.OK(t, "inventory", "delete", first.ID, "--yes")
	var left inventory
	pcpctl.JSON(t, &left, "inventory", "get", name)
	require.Equal(t, second.ID, left.ID)
}

// TestInventoryIsolation: an inventory lives in one organization. From a
// sub-organization it is neither listed nor readable, and a delete from there
// is a not-found rather than a deletion.
func TestInventoryIsolation(t *testing.T) {
	requireInstance(t)

	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	require.NotContains(t, inventoryIDs(t, in), inv.ID)
	in.Fails(t, harness.ExitNotFound, "inventory", "get", inv.ID)
	in.Fails(t, harness.ExitNotFound, "inventory", "delete", inv.ID, "--yes")
	pcpctl.OK(t, "inventory", "get", inv.ID)
}

// TestResourceLifecycle fills a self-managed inventory by hand: add a
// resource, read it, see it in the inventory's listing and in the listing of
// every inventory, count it on the inventory, remove it, and see it gone.
func TestResourceLifecycle(t *testing.T) {
	requireInstance(t)

	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	require.Equal(t, 0, inventoryRow(t, pcpctl, inv.ID).Count)

	r := addResource(t, pcpctl, inv.ID, "laptop", "--subclass", "vm", "--service", "ec2", "--tag", "qa", "--tag", "lab")
	require.NotEmpty(t, r.URNID)
	require.Equal(t, "laptop", r.Name)
	require.Equal(t, "compute", r.Class)
	require.Equal(t, "vm", r.Subclass)
	require.Equal(t, "ec2", r.Service)
	require.ElementsMatch(t, []string{"qa", "lab"}, r.Tags)

	var got resource
	pcpctl.JSON(t, &got, "inventory", "resources", "get", inv.ID, r.URNID)
	require.Equal(t, r.URNID, got.URNID)
	require.Equal(t, inv.ID, got.InventoryID)
	require.Equal(t, "laptop", got.Name)

	pcpctl.JSON(t, &got, "inventory", "resources", "get", inv.Name, r.URNID)
	require.Equal(t, r.URNID, got.URNID, "the inventory resolves by name")

	require.Equal(t, []string{r.URNID}, resourceURNIDs(t, pcpctl, inv.ID))
	require.Contains(t, resourceURNIDs(t, pcpctl, "--all"), r.URNID)

	// The count is a coverage rollup and follows the add shortly after.
	require.Eventually(t, func() bool { return inventoryRow(t, pcpctl, inv.ID).Count == 1 },
		30*time.Second, 2*time.Second, "the listing counts what the inventory holds")

	out := pcpctl.OK(t, "inventory", "resources", "delete", inv.ID, r.URNID, "--yes")
	require.Contains(t, out, r.URNID)
	pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "get", inv.ID, r.URNID)
	require.Empty(t, resourceURNIDs(t, pcpctl, inv.ID))
}

// TestResourceMisuse: adding with nothing to name the resource, into an
// inventory that does not exist, and the removals that must be refused.
func TestResourceMisuse(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))

	t.Run("nothing to name it", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "inventory", "resources", "add", inv.ID, "--class", "compute")
	})
	t.Run("absent inventory", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "add", missing, "ghost", "--class", "compute")
	})
	t.Run("entry that is not JSON", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "inventory", "resources", "add", inv.ID, "--entry", "{nope")
	})
	t.Run("duplicate urn", func(t *testing.T) {
		r := addResource(t, pcpctl, inv.ID, "first")
		pcpctl.Fails(t, harness.ExitConflict, "inventory", "resources", "add", inv.ID, "second",
			"--class", "compute", "--urn", r.URN)
		require.Len(t, resourceURNIDs(t, pcpctl, inv.ID), 1, "a refused add adds nothing")
		pcpctl.OK(t, "inventory", "resources", "delete", inv.ID, r.URNID, "--yes")
	})
	t.Run("get absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "get", inv.ID, missing)
	})
	t.Run("delete absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "delete", inv.ID, missing, "--yes")
	})
	t.Run("delete without --yes", func(t *testing.T) {
		r := addResource(t, pcpctl, inv.ID, "kept")
		pcpctl.Fails(t, harness.ExitError, "inventory", "resources", "delete", inv.ID, r.URNID)
		pcpctl.OK(t, "inventory", "resources", "get", inv.ID, r.URNID)
	})
	t.Run("delete twice", func(t *testing.T) {
		r := addResource(t, pcpctl, inv.ID, "gone")
		pcpctl.OK(t, "inventory", "resources", "delete", inv.ID, r.URNID, "--yes")
		pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "delete", inv.ID, r.URNID, "--yes")
	})
}

// TestResourceUpdate renames a resource, replaces its tags, and reads the
// change back; an update of nothing is a not-found.
func TestResourceUpdate(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	r := addResource(t, pcpctl, inv.ID, "before", "--tag", "old")

	var updated resource
	pcpctl.JSON(t, &updated, "inventory", "resources", "update", inv.ID, r.URNID, "--name", "after", "--tag", "new")
	require.Equal(t, r.URNID, updated.URNID, "the urn id survives an update")
	require.Equal(t, "after", updated.Name)
	require.Equal(t, []string{"new"}, updated.Tags)

	var got resource
	pcpctl.JSON(t, &got, "inventory", "resources", "get", inv.ID, r.URNID)
	require.Equal(t, "after", got.Name)
	require.Equal(t, []string{"new"}, got.Tags)

	pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "update", inv.ID, missing, "--name", "ghost")
}

// TestResourceLock takes and releases the lock on a resource, and reads the
// state back each time.
func TestResourceLock(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	r := addResource(t, pcpctl, inv.ID, "lockable")
	require.False(t, r.Locked)

	require.Contains(t, pcpctl.OK(t, "inventory", "resources", "lock", inv.ID, r.URNID), "Locked: true")
	var got resource
	pcpctl.JSON(t, &got, "inventory", "resources", "get", inv.ID, r.URNID)
	require.True(t, got.Locked)

	require.Contains(t, pcpctl.OK(t, "inventory", "resources", "lock", inv.ID, r.URNID, "--release"), "Locked: false")
	pcpctl.JSON(t, &got, "inventory", "resources", "get", inv.ID, r.URNID)
	require.False(t, got.Locked)

	pcpctl.Fails(t, harness.ExitNotFound, "inventory", "resources", "lock", inv.ID, missing)
}

// TestInventoryResidency pins where an inventory's data may live, sees the
// resources inherit it, overrides it on one resource, and refuses a place
// that is not a country.
func TestInventoryResidency(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	r := addResource(t, pcpctl, inv.ID, "placed")

	require.Contains(t, pcpctl.OK(t, "inventory", "residency", inv.ID, "FR"), "FR")
	var got inventory
	pcpctl.JSON(t, &got, "inventory", "get", inv.ID)
	require.Equal(t, "FR", got.Residency)

	var res resource
	pcpctl.JSON(t, &res, "inventory", "resources", "get", inv.ID, r.URNID)
	require.Equal(t, "FR", res.Residency, "a resource inherits its inventory's residency")

	require.Contains(t, pcpctl.OK(t, "inventory", "resources", "residency", inv.ID, r.URNID, "DE"), "DE")
	pcpctl.JSON(t, &res, "inventory", "resources", "get", inv.ID, r.URNID)
	require.Equal(t, "DE", res.Residency, "a resource may override it")

	pcpctl.Fails(t, harness.ExitError, "inventory", "residency", inv.ID, "Narnia")
	pcpctl.JSON(t, &got, "inventory", "get", inv.ID)
	require.Equal(t, "FR", got.Residency, "a refused residency changes nothing")

	pcpctl.Fails(t, harness.ExitNotFound, "inventory", "residency", missing, "FR")
}

// TestInventoryClassesAndCoverage: an inventory answers the class tree a
// resource may be filed under, and the coverage count sees a resource with
// nothing protecting it as unprotected.
func TestInventoryClassesAndCoverage(t *testing.T) {
	requireInstance(t)
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))

	var before struct {
		Protected, Unprotected, Excluded int
	}
	pcpctl.JSON(t, &before, "inventory", "coverage")

	var classes map[string][]string
	pcpctl.JSON(t, &classes, "inventory", "classes", inv.ID)
	require.Contains(t, classes, "compute")
	require.Contains(t, classes["object-storage"], "s3")

	addResource(t, pcpctl, inv.ID, "bare", "--subclass", "vm")

	var after struct {
		Protected, Unprotected, Excluded int
	}
	pcpctl.JSON(t, &after, "inventory", "coverage")
	require.Equal(t, before.Unprotected+1, after.Unprotected, "an unprotected resource counts as such")
	require.Equal(t, before.Protected, after.Protected)
}
