// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv1

import (
	"strings"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

// organization is the row pcpctl prints for one.
type organization struct {
	ID       string            `json:"id"`
	ParentID *string           `json:"parent_id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Info     map[string]string `json:"info"`
	Revision int               `json:"revision"`
}

// createOrg creates a sub-organization of the one in scope and removes it
// when the test ends, whatever the test did with it.
func createOrg(t *testing.T, p *harness.Pcpctl, name string, extra ...string) organization {
	t.Helper()
	var org organization
	p.JSON(t, &org, append([]string{"organization", "create", name, "--type", "enterprise"}, extra...)...)
	t.Cleanup(func() { p.Run(t, "organization", "delete", org.ID, "--yes") })
	return org
}

func listOrgs(t *testing.T, p *harness.Pcpctl, args ...string) []organization {
	t.Helper()
	var orgs []organization
	p.JSON(t, &orgs, append([]string{"organization"}, args...)...)
	return orgs
}

func orgIDs(orgs []organization) []string {
	ids := make([]string, len(orgs))
	for i, o := range orgs {
		ids[i] = o.ID
	}
	return ids
}

// TestOrganizationLifecycle creates an organization, reads it back by id and
// by name, finds it in the listing and among the children of the one in
// scope, deletes it, and checks every read then says it is gone.
func TestOrganizationLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-org")

	var created organization
	pcpctl.JSON(t, &created, "organization", "create", name, "--type", "enterprise", "--info", "team=qa")
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	require.Equal(t, "enterprise", created.Type)
	require.NotNil(t, created.ParentID, "a sub-organization names its parent")
	require.Equal(t, inst.OrgID.String(), *created.ParentID, "it is created under the organization in scope")
	require.Equal(t, "qa", created.Info["team"])

	var byID organization
	pcpctl.JSON(t, &byID, "organization", "get", created.ID)
	require.Equal(t, created, byID)

	var byName organization
	pcpctl.JSON(t, &byName, "organization", "get", name)
	require.Equal(t, created.ID, byName.ID, "get resolves a name to the same organization")

	require.Contains(t, orgIDs(listOrgs(t, pcpctl, "list")), created.ID)
	require.Contains(t, orgIDs(listOrgs(t, pcpctl, "children")), created.ID)

	out := pcpctl.OK(t, "organization", "delete", created.ID, "--yes")
	require.Contains(t, out, created.ID, "the deletion names what went")

	pcpctl.Fails(t, harness.ExitNotFound, "organization", "get", created.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "organization", "get", name)
	require.NotContains(t, orgIDs(listOrgs(t, pcpctl, "list")), created.ID)
	require.NotContains(t, orgIDs(listOrgs(t, pcpctl, "children")), created.ID)
}

// TestOrganizationDeleteMisuse covers the deletions that must be refused: one
// of nothing, one of something already gone, one without --yes, and one of
// the organization the key itself acts in.
func TestOrganizationDeleteMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "organization", "delete", missing, "--yes")
	})
	t.Run("twice", func(t *testing.T) {
		org := createOrg(t, pcpctl, harness.Unique("qa-org"))
		pcpctl.OK(t, "organization", "delete", org.ID, "--yes")
		pcpctl.Fails(t, harness.ExitNotFound, "organization", "delete", org.ID, "--yes")
	})
	t.Run("without --yes", func(t *testing.T) {
		org := createOrg(t, pcpctl, harness.Unique("qa-org"))
		res := pcpctl.Fails(t, harness.ExitError, "organization", "delete", org.ID)
		require.Contains(t, res.Stderr, org.Name, "the refusal names what it would have deleted")
		pcpctl.OK(t, "organization", "get", org.ID)
	})
	t.Run("the one in scope", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitConflict, "organization", "delete", inst.OrgID.String(), "--yes")
		pcpctl.OK(t, "organization", "get", inst.OrgID.String())
	})
}

// TestOrganizationDeleteWithChildren: an organization with a sub-organization
// is not deleted out from under it; once the child is gone the parent can go.
func TestOrganizationDeleteWithChildren(t *testing.T) {
	requireInstance(t)

	parent := createOrg(t, pcpctl, harness.Unique("qa-parent"))
	child := createOrg(t, pcpctl, harness.Unique("qa-child"), "--parent", parent.ID)
	require.Equal(t, parent.ID, *child.ParentID)
	require.Contains(t, orgIDs(listOrgs(t, pcpctl.In(parent.ID), "children")), child.ID)

	pcpctl.Fails(t, harness.ExitConflict, "organization", "delete", parent.ID, "--yes")
	pcpctl.OK(t, "organization", "get", child.ID)

	pcpctl.OK(t, "organization", "delete", child.ID, "--yes")
	pcpctl.OK(t, "organization", "delete", parent.ID, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "organization", "get", parent.ID)
}

// TestOrganizationCreateMisuse: the creations that must be refused, each with
// the exit code a script would branch on.
func TestOrganizationCreateMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("no type", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "organization", "create", harness.Unique("qa-org"))
	})
	t.Run("no name", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "organization", "create", "--type", "enterprise")
	})
	t.Run("unknown type", func(t *testing.T) {
		name := harness.Unique("qa-org")
		pcpctl.Fails(t, harness.ExitError, "organization", "create", name, "--type", "workspace")
		pcpctl.Fails(t, harness.ExitNotFound, "organization", "get", name)
	})
	t.Run("absent parent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "organization", "create", harness.Unique("qa-org"),
			"--type", "enterprise", "--parent", missing)
	})
	t.Run("parent by unknown name", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "organization", "create", harness.Unique("qa-org"),
			"--type", "enterprise", "--parent", harness.Unique("no-such-org"))
	})
}

// TestOrganizationDuplicateName: names are not unique, so a second create
// with the same name succeeds as a distinct organization, and a get by that
// name is then refused as ambiguous rather than picking one.
func TestOrganizationDuplicateName(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-twin")

	first := createOrg(t, pcpctl, name)
	second := createOrg(t, pcpctl, name)
	require.NotEqual(t, first.ID, second.ID)

	ids := orgIDs(listOrgs(t, pcpctl, "children"))
	require.Contains(t, ids, first.ID)
	require.Contains(t, ids, second.ID)

	res := pcpctl.Fails(t, harness.ExitError, "organization", "get", name)
	require.Contains(t, strings.ToLower(res.Stderr), name, "the refusal names the ambiguous name")

	pcpctl.OK(t, "organization", "delete", first.ID, "--yes")
	var left organization
	pcpctl.JSON(t, &left, "organization", "get", name)
	require.Equal(t, second.ID, left.ID, "with one left, the name resolves again")
}

// TestOrganizationScoping: --organization re-scopes every command to a
// sub-organization, downward only. A listing in the child shows the child's
// world, and naming something that is not below the key's organization is a
// not-found, never a sibling tenant.
func TestOrganizationScoping(t *testing.T) {
	requireInstance(t)

	child := createOrg(t, pcpctl, harness.Unique("qa-scope"))
	grandchild := createOrg(t, pcpctl.In(child.ID), harness.Unique("qa-scope-child"))
	require.Equal(t, child.ID, *grandchild.ParentID, "created under the organization in scope")

	require.Equal(t, []string{grandchild.ID}, orgIDs(listOrgs(t, pcpctl.In(child.ID), "children")))
	require.Equal(t, []string{grandchild.ID}, orgIDs(listOrgs(t, pcpctl.In(child.Name), "children")),
		"--organization takes a name as well as an id")

	var whoami struct {
		OrganizationID string `json:"organization_id"`
	}
	pcpctl.In(child.ID).JSON(t, &whoami, "whoami")
	require.Equal(t, child.ID, whoami.OrganizationID, "the badge is re-scoped to the child")

	t.Run("absent organization", func(t *testing.T) {
		// Scoping is a badge for that organization, and a badge for one the
		// key cannot reach is refused as such.
		pcpctl.In(missing).Fails(t, harness.ExitUnauthorized, "organization", "children")
	})
	t.Run("unknown name", func(t *testing.T) {
		pcpctl.In(harness.Unique("no-such")).Fails(t, harness.ExitNotFound, "organization", "children")
	})
	t.Run("not below the key", func(t *testing.T) {
		// The grandchild cannot reach up to its parent: scoping goes downward.
		pcpctl.In(grandchild.ID).Fails(t, harness.ExitNotFound, "organization", "get", child.ID)
	})
}

// TestOrganizationCapabilities: what the caller may do, class by class, is
// one question with the server's shape.
func TestOrganizationCapabilities(t *testing.T) {
	requireInstance(t)

	var caps map[string][]string
	pcpctl.JSON(t, &caps, "organization", "capabilities")
	require.Contains(t, caps, "inventory")
	require.Contains(t, caps["inventory"], "view", "an owner may at least view inventories")
	require.Contains(t, caps, "organization")
}

// TestOrganizationListFull spans every tree the account holds a role in,
// which includes the one in scope.
func TestOrganizationListFull(t *testing.T) {
	requireInstance(t)

	require.Contains(t, orgIDs(listOrgs(t, pcpctl, "list", "--full")), inst.OrgID.String())
}
