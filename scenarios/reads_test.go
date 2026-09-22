package scenarios

import (
	"fmt"
	"testing"

	api "github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/stretchr/testify/require"
)

// TestReadSurface calls the read endpoints nothing else calls. The ledger said
// these had never been requested by any client, which means nobody would have
// noticed them breaking.
func TestReadSurface(t *testing.T) {
	requireInstance(t)

	org := orgID.String()
	for _, tc := range []struct{ name, path string }{
		{"readiness", "/health/ready"},
		{"users", "/api/v1/users"},
		{"residencies", "/api/v1/residencies"},
		{"resource classes", "/api/v1/resource_classes"},
		{"inventory coverage", "/api/v1/inventories/coverage"},
		{"installed integrations", "/api/v1/integrations/installed"},
		{"packages", "/api/v1/packages"},
		{"scheduler roles", "/api/v1/scheduling/scheduler/roles?actor=default"},
		{"journal export", "/api/v1/journal/export"},
		{"debuglog", "/api/v1/debuglog"},
		{"update check", "/api/v1/has-update"},
		{"settings sections", "/api/v1/account/organizations/" + org + "/settings/sections"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := answers(t, "GET", tc.path)
			t.Logf("GET %s -> %d", tc.path, code)
		})
	}
}

// TestIntegrationDetail walks from the installed list into the per-integration
// reads, which need an id that only the list can give.
func TestIntegrationDetail(t *testing.T) {
	requireInstance(t)

	var installed api.PaginatedResponse[struct {
		Id   string `json:"id"`
		Name string `json:"name"`
	}]
	require.NoError(t, owner.EndpointWithLimitOffset("/api/v1/integrations/installed", 100, 0).Get(&installed))
	require.NotEmpty(t, installed.Items, "the rig seeds s3, so something is installed")

	id := installed.Items[0].Id
	name := installed.Items[0].Name

	t.Run("by id", func(t *testing.T) {
		answers(t, "GET", "/api/v1/integrations/installed/"+id)
	})
	t.Run("summary", func(t *testing.T) {
		answers(t, "GET", "/api/v1/integrations/installed/"+id+"/summary")
	})
	t.Run("available by name", func(t *testing.T) {
		answers(t, "GET", "/api/v1/integrations/available/"+name)
	})
	t.Run("catalog versions", func(t *testing.T) {
		answers(t, "GET", "/api/v1/integrations/catalog/"+name+"/versions")
	})
}

// TestResourceEndpoints reaches the per-resource endpoint listing, which needs
// an inventory and a resource in it.
func TestResourceEndpoints(t *testing.T) {
	requireInstance(t)

	var invs api.PaginatedResponse[api.InventoryListResult]
	require.NoError(t, owner.EndpointWithLimitOffset(
		"/api/v1/account/organizations/"+orgID.String()+"/inventories", 100, 0).Get(&invs))
	require.NotEmpty(t, invs.Items, "the rig seeds a self-managed inventory")
	inv := invs.Items[0].ID.String()

	var resources api.PaginatedResponse[api.ResourceEntry]
	require.NoError(t, owner.EndpointWithLimitOffset(
		fmt.Sprintf("/api/v1/inventories/%s/resources", inv), 100, 0).Get(&resources))
	if len(resources.Items) == 0 {
		t.Skip("no resources in the seeded inventory; the e2e scenarios create and remove their own")
	}

	answers(t, "GET", fmt.Sprintf("/api/v1/inventories/%s/resources/%s/endpoints",
		inv, resources.Items[0].URNID))
}
