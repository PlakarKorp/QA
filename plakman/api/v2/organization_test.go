// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"net/http"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type organization struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
}

// createOrg creates a sub-organization under parent (the caller's when empty)
// and removes it, with whatever it still holds, when the test ends.
func createOrg(t *testing.T, c *harness.V2, name, parent string) organization {
	t.Helper()
	body := map[string]any{"name": name, "type": "enterprise"}
	if parent != "" {
		body["parent_id"] = parent
	}
	var org organization
	c.OK(t, http.MethodPost, "/api/v2/organizations", body, &org)
	t.Cleanup(func() { c.Do(t, http.MethodDelete, "/api/v2/organizations/"+org.ID+"?force=true", nil) })
	return org
}

func childIDs(t *testing.T, c *harness.V2, parent string) []string {
	t.Helper()
	page := harness.List[organization](t, c, "/api/v2/organizations?limit=200&parent_id="+parent)
	ids := make([]string, len(page.Items))
	for i, o := range page.Items {
		ids[i] = o.ID
	}
	return ids
}

// scoped trades the badge for one naming another organization, which is what
// pcpctl's --organization does.
func scoped(t *testing.T, c *harness.V2, org string) *harness.V2 {
	t.Helper()
	var token struct {
		Token string `json:"token"`
	}
	c.OK(t, http.MethodPost, "/api/v2/auth/badge", map[string]string{"organization_id": org}, &token)
	return c.As(token.Token)
}

// TestOrganizationLifecycle creates an organization, reads it back, finds it
// among its parent's children, deletes it, and sees every read say it is
// gone. v2 has no lookup by name: a name is a filter a client applies to the
// listing, and here it is found that way.
func TestOrganizationLifecycle(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-org")

	var created organization
	v2.OK(t, http.MethodPost, "/api/v2/organizations", map[string]any{"name": name, "type": "enterprise"}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	require.Equal(t, "enterprise", created.Type)
	require.Equal(t, inst.OrgID.String(), created.ParentID, "created under the caller's organization")

	var byID organization
	v2.OK(t, http.MethodGet, "/api/v2/organizations/"+created.ID, nil, &byID)
	require.Equal(t, created, byID)

	children := harness.List[organization](t, v2, "/api/v2/organizations?limit=200&parent_id="+inst.OrgID.String())
	var byName []organization
	for _, o := range children.Items {
		if o.Name == name {
			byName = append(byName, o)
		}
	}
	require.Len(t, byName, 1)
	require.Equal(t, created.ID, byName[0].ID)

	var deleted struct {
		DeletedAt string `json:"deleted_at"`
	}
	v2.OK(t, http.MethodDelete, "/api/v2/organizations/"+created.ID, nil, &deleted)
	require.NotEmpty(t, deleted.DeletedAt)

	v2.Refused(t, http.MethodGet, "/api/v2/organizations/"+created.ID, nil, harness.CodeNotFound)
	require.NotContains(t, childIDs(t, v2, inst.OrgID.String()), created.ID)
}

// TestOrganizationDeleteMisuse: deleting nothing, deleting twice, and
// deleting the organization the badge acts in. v2 has no --yes: the
// confirmation is the client's, so that case has no wire mirror.
func TestOrganizationDeleteMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("absent", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/organizations/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("malformed id", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/organizations/not-an-id", nil, harness.CodeInvalidArgument)
	})
	t.Run("twice", func(t *testing.T) {
		org := createOrg(t, v2, harness.Unique("qa-org"), "")
		v2.OK(t, http.MethodDelete, "/api/v2/organizations/"+org.ID, nil, nil)
		v2.Refused(t, http.MethodDelete, "/api/v2/organizations/"+org.ID, nil, harness.CodeNotFound)
	})
	t.Run("the one in scope", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/organizations/"+inst.OrgID.String(), nil, harness.CodeConflict)
		v2.OK(t, http.MethodGet, "/api/v2/organizations/"+inst.OrgID.String(), nil, nil)
	})
}

// TestOrganizationDeleteWithChildren: a parent is not deleted out from under
// its child, unless forced; with the child gone it can go.
func TestOrganizationDeleteWithChildren(t *testing.T) {
	requireInstance(t)

	parent := createOrg(t, v2, harness.Unique("qa-parent"), "")
	child := createOrg(t, v2, harness.Unique("qa-child"), parent.ID)
	require.Equal(t, parent.ID, child.ParentID)
	require.Equal(t, []string{child.ID}, childIDs(t, v2, parent.ID))

	v2.Refused(t, http.MethodDelete, "/api/v2/organizations/"+parent.ID, nil, harness.CodeConflict)
	v2.OK(t, http.MethodGet, "/api/v2/organizations/"+child.ID, nil, nil)

	v2.OK(t, http.MethodDelete, "/api/v2/organizations/"+child.ID, nil, nil)
	v2.OK(t, http.MethodDelete, "/api/v2/organizations/"+parent.ID, nil, nil)
	v2.Refused(t, http.MethodGet, "/api/v2/organizations/"+parent.ID, nil, harness.CodeNotFound)

	t.Run("forced", func(t *testing.T) {
		parent := createOrg(t, v2, harness.Unique("qa-parent"), "")
		child := createOrg(t, v2, harness.Unique("qa-child"), parent.ID)
		v2.OK(t, http.MethodDelete, "/api/v2/organizations/"+parent.ID+"?force=true", nil, nil)
		v2.Refused(t, http.MethodGet, "/api/v2/organizations/"+child.ID, nil, harness.CodeNotFound)
	})
}

// TestOrganizationCreateMisuse: the creations that must be refused, each
// with the documented code, and nothing created by any of them.
func TestOrganizationCreateMisuse(t *testing.T) {
	requireInstance(t)
	before := len(childIDs(t, v2, inst.OrgID.String()))

	t.Run("no type", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/organizations", map[string]any{"name": harness.Unique("qa-org")}, harness.CodeInvalidArgument)
	})
	t.Run("no name", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/organizations", map[string]any{"type": "enterprise"}, harness.CodeInvalidArgument)
	})
	t.Run("unknown type", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/organizations", map[string]any{"name": harness.Unique("qa-org"), "type": "workspace"}, harness.CodeInvalidArgument)
	})
	t.Run("absent parent", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/organizations", map[string]any{"name": harness.Unique("qa-org"), "type": "enterprise", "parent_id": missing}, harness.CodeNotFound)
	})
	t.Run("malformed parent", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/organizations", map[string]any{"name": harness.Unique("qa-org"), "type": "enterprise", "parent_id": "nope"}, harness.CodeInvalidArgument)
	})
	t.Run("body that is not JSON", func(t *testing.T) {
		status, _ := v2.Do(t, http.MethodPost, "/api/v2/organizations", "not json")
		require.Equal(t, http.StatusBadRequest, status)
	})
	require.Len(t, childIDs(t, v2, inst.OrgID.String()), before, "a refused create creates nothing")
}

// TestOrganizationDuplicateName: names are not unique, so two organizations
// may share one, and a client filtering the listing by name sees both.
func TestOrganizationDuplicateName(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-twin")

	first := createOrg(t, v2, name, "")
	second := createOrg(t, v2, name, "")
	require.NotEqual(t, first.ID, second.ID)

	var twins []string
	for _, o := range harness.List[organization](t, v2, "/api/v2/organizations?limit=200&parent_id="+inst.OrgID.String()).Items {
		if o.Name == name {
			twins = append(twins, o.ID)
		}
	}
	require.ElementsMatch(t, []string{first.ID, second.ID}, twins)
}

// TestOrganizationScoping: a badge re-scoped to a sub-organization sees that
// organization's world, and the re-scoping only goes downward: an
// organization the badge cannot reach, or none at all, is refused.
func TestOrganizationScoping(t *testing.T) {
	requireInstance(t)

	child := createOrg(t, v2, harness.Unique("qa-scope"), "")
	in := scoped(t, v2, child.ID)
	grandchild := createOrg(t, in, harness.Unique("qa-scope-child"), "")
	require.Equal(t, child.ID, grandchild.ParentID, "created under the organization in scope")
	require.Equal(t, []string{grandchild.ID}, childIDs(t, in, child.ID))

	var session struct {
		OrganizationID string `json:"organization_id"`
		UserID         string `json:"user_id"`
	}
	in.OK(t, http.MethodGet, "/api/v2/auth/session", nil, &session)
	require.Equal(t, child.ID, session.OrganizationID, "the badge is re-scoped to the child")
	require.Equal(t, inst.UserID.String(), session.UserID)

	t.Run("absent organization", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/auth/badge", map[string]string{"organization_id": missing}, harness.CodeNotFound)
	})
	t.Run("malformed organization", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/auth/badge", map[string]string{"organization_id": "nope"}, harness.CodeInvalidArgument)
	})
	t.Run("not below the badge", func(t *testing.T) {
		// A badge scoped below cannot read upward. It can be re-scoped to the
		// parent, though: v2 re-scopes to any organization the caller holds
		// an effective role in, and the administrator holds one at the root.
		below := scoped(t, v2, grandchild.ID)
		below.Refused(t, http.MethodGet, "/api/v2/organizations/"+child.ID, nil, harness.CodeNotFound)
		var token struct {
			Token string `json:"token"`
		}
		below.OK(t, http.MethodPost, "/api/v2/auth/badge", map[string]string{"organization_id": child.ID}, &token)
		var session struct {
			OrganizationID string `json:"organization_id"`
		}
		below.As(token.Token).OK(t, http.MethodGet, "/api/v2/auth/session", nil, &session)
		require.Equal(t, child.ID, session.OrganizationID)
	})
}

// TestOrganizationCapabilities: what the caller may do, class by class, in
// one question. v2 answers it through the session's roles and the role
// catalogue, which say what each role confers.
func TestOrganizationCapabilities(t *testing.T) {
	requireInstance(t)

	var session struct {
		Roles []string `json:"roles"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/auth/session", nil, &session)
	require.NotEmpty(t, session.Roles, "the administrator holds a role")

	var role struct {
		Name   string `json:"name"`
		Grants []struct {
			ResourceType string `json:"resource_type"`
			Access       struct {
				CanView bool `json:"can_view"`
			} `json:"access"`
		} `json:"grants"`
	}
	v2.OK(t, http.MethodGet, "/api/v2/roles/"+session.Roles[0], nil, &role)
	require.Equal(t, session.Roles[0], role.Name)
	var inventory bool
	for _, g := range role.Grants {
		if g.ResourceType == "inventory" {
			inventory = g.Access.CanView
		}
	}
	require.True(t, inventory, "the administrator's role lets them view inventories")
}

// TestOrganizationListFull: the listing spans what the badge sees, the
// organization in scope included.
func TestOrganizationListFull(t *testing.T) {
	requireInstance(t)

	page := harness.List[organization](t, v2, "/api/v2/organizations?limit=200")
	var ids []string
	for _, o := range page.Items {
		ids = append(ids, o.ID)
	}
	require.Contains(t, ids, inst.OrgID.String())
}
