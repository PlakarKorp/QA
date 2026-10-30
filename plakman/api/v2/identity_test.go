// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type user struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsService bool   `json:"is_service"`
}

type membership struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
}

type group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type grant struct {
	ID      string `json:"id"`
	Subject struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"subject"`
	Role struct {
		Name string `json:"name"`
	} `json:"role"`
}

type session struct {
	UserID         string   `json:"user_id"`
	OrganizationID string   `json:"organization_id"`
	Roles          []string `json:"roles"`
	AuthMethod     string   `json:"auth_method"`
}

// createUser provisions a user in the organization in scope, member at once,
// which is v2's one-call form of v1's auto-accepted invitation. The user is
// global and outlives the membership, so nothing removes it.
func createUser(t *testing.T, c *harness.V2, userType string) user {
	t.Helper()
	name := harness.Unique("qa-user")
	body := map[string]any{"email": name + "@example.com", "name": name}
	if userType != "" {
		body["type"] = userType
	}
	var created struct {
		User     user   `json:"user"`
		Password string `json:"password"`
	}
	c.OK(t, http.MethodPost, "/api/v2/users", body, &created)
	require.NotEmpty(t, created.User.ID)
	t.Cleanup(func() {
		for _, m := range memberships(t, c, "&user_id="+created.User.ID) {
			c.Do(t, http.MethodDelete, "/api/v2/memberships/"+m.ID, nil)
		}
	})
	return created.User
}

func memberships(t *testing.T, c *harness.V2, query string) []membership {
	t.Helper()
	return harness.List[membership](t, c, "/api/v2/memberships?limit=200"+query).Items
}

func memberIDs(t *testing.T, c *harness.V2, org string) []string {
	t.Helper()
	var ids []string
	for _, m := range memberships(t, c, "&organization_id="+org) {
		ids = append(ids, m.UserID)
	}
	return ids
}

// TestWhoami: the badge answers for the administrator the key was minted
// for, in the organization it was issued in, and says it came from a key.
func TestWhoami(t *testing.T) {
	requireInstance(t)

	var me session
	v2.OK(t, http.MethodGet, "/api/v2/auth/session", nil, &me)
	require.Equal(t, inst.UserID.String(), me.UserID)
	require.Equal(t, inst.OrgID.String(), me.OrganizationID)
	require.Equal(t, "apikey", me.AuthMethod)

	var u user
	v2.OK(t, http.MethodGet, "/api/v2/users/"+me.UserID, nil, &u)
	require.Equal(t, harness.AdminEmail, u.Email)
}

// TestAuthRejectsBadKey: a key that is not one, a token that is not one, and
// no token at all are refused as unauthenticated.
func TestAuthRejectsBadKey(t *testing.T) {
	requireInstance(t)

	anonymous := v2.As("")
	anonymous.Refused(t, http.MethodPost, "/api/v2/auth/login/apikey", map[string]string{"api_key": "pcp_ak_nope.nope"}, harness.CodeUnauthenticated)
	anonymous.Refused(t, http.MethodPost, "/api/v2/auth/login/apikey", map[string]string{}, harness.CodeInvalidArgument)
	anonymous.Refused(t, http.MethodGet, "/api/v2/auth/session", nil, harness.CodeUnauthenticated)
	v2.As("not-a-token").Refused(t, http.MethodGet, "/api/v2/organizations", nil, harness.CodeUnauthenticated)
}

// TestUserDirectory: a provisioned user is in the directory, readable by id
// and found by email through the search, and someone absent is a not-found.
func TestUserDirectory(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")
	require.False(t, u.IsService)
	require.True(t, strings.HasSuffix(u.Email, "@example.com"))

	var byID user
	v2.OK(t, http.MethodGet, "/api/v2/users/"+u.ID, nil, &byID)
	require.Equal(t, u, byID)

	found := harness.List[user](t, v2, "/api/v2/users?search="+u.Email)
	require.Len(t, found.Items, 1)
	require.Equal(t, u.ID, found.Items[0].ID, "search finds the user by email")

	v2.Refused(t, http.MethodGet, "/api/v2/users/"+missing, nil, harness.CodeNotFound)
	require.Empty(t, harness.List[user](t, v2, "/api/v2/users?search=nobody-"+harness.Unique("x")).Items)
}

// TestUserRename: a name is the person's own; a badge from an API key may
// not change it, theirs or anyone else's, and the name stays.
func TestUserRename(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")

	v2.Refused(t, http.MethodPatch, "/api/v2/users/"+u.ID, map[string]any{"name": "Renamed Person"}, harness.CodePermissionDenied)
	v2.Refused(t, http.MethodPatch, "/api/v2/users/"+inst.UserID.String(), map[string]any{"name": "Renamed Admin"}, harness.CodePermissionDenied)

	var got user
	v2.OK(t, http.MethodGet, "/api/v2/users/"+u.ID, nil, &got)
	require.Equal(t, u.Name, got.Name)
}

// TestServiceUser: an application account is created as such, gets no
// password, and is marked service in the directory and the listing.
func TestServiceUser(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-service")
	var created struct {
		User     user   `json:"user"`
		Password string `json:"password"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/users", map[string]any{"email": name + "@example.com", "name": name, "type": "service"}, &created)
	require.True(t, created.User.IsService)
	require.Empty(t, created.Password, "an application has no password")
	t.Cleanup(func() {
		for _, m := range memberships(t, v2, "&user_id="+created.User.ID) {
			v2.Do(t, http.MethodDelete, "/api/v2/memberships/"+m.ID, nil)
		}
	})

	services := harness.List[user](t, v2, "/api/v2/users?limit=200&type=service")
	var found bool
	for _, u := range services.Items {
		found = found || u.ID == created.User.ID
	}
	require.True(t, found, "the service filter lists it")
	require.Contains(t, memberIDs(t, v2, inst.OrgID.String()), created.User.ID)
}

// TestMembership adds a user to a sub-organization, lists them there,
// removes them, and refuses the memberships that make no sense.
func TestMembership(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	require.NotContains(t, memberIDs(t, v2, child.ID), u.ID)

	var m membership
	v2.OK(t, http.MethodPost, "/api/v2/memberships", map[string]string{"organization_id": child.ID, "user_id": u.ID}, &m)
	require.NotEmpty(t, m.ID)
	require.Equal(t, child.ID, m.OrganizationID)
	require.Equal(t, u.ID, m.UserID)
	require.Contains(t, memberIDs(t, v2, child.ID), u.ID)
	var got membership
	v2.OK(t, http.MethodGet, "/api/v2/memberships/"+m.ID, nil, &got)
	require.Equal(t, m, got)

	t.Run("add twice", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/memberships", map[string]string{"organization_id": child.ID, "user_id": u.ID}, harness.CodeConflict)
	})
	t.Run("add nobody", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/memberships", map[string]string{"organization_id": child.ID, "user_id": missing}, harness.CodeNotFound)
	})
	t.Run("add to nowhere", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/memberships", map[string]string{"organization_id": missing, "user_id": u.ID}, harness.CodeNotFound)
	})
	t.Run("remove oneself", func(t *testing.T) {
		own := memberships(t, v2, "&organization_id="+inst.OrgID.String()+"&user_id="+inst.UserID.String())
		require.Len(t, own, 1)
		v2.Refused(t, http.MethodDelete, "/api/v2/memberships/"+own[0].ID, nil, harness.CodeConflict)
	})

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/memberships/"+m.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
	require.NotContains(t, memberIDs(t, v2, child.ID), u.ID)
	v2.OK(t, http.MethodGet, "/api/v2/users/"+u.ID, nil, nil) // the user outlives the membership

	t.Run("remove twice", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/memberships/"+m.ID, nil, harness.CodeNotFound)
	})
}

// TestOrganizationDeleteWithMembers: an organization that still has other
// members is not deleted, unless forced; with them gone it can be.
func TestOrganizationDeleteWithMembers(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")
	child := createOrg(t, v2, harness.Unique("qa-org"), "")
	var m membership
	v2.OK(t, http.MethodPost, "/api/v2/memberships", map[string]string{"organization_id": child.ID, "user_id": u.ID}, &m)

	v2.Refused(t, http.MethodDelete, "/api/v2/organizations/"+child.ID, nil, harness.CodeConflict)
	v2.OK(t, http.MethodGet, "/api/v2/organizations/"+child.ID, nil, nil)

	v2.OK(t, http.MethodDelete, "/api/v2/memberships/"+m.ID, nil, nil)
	v2.OK(t, http.MethodDelete, "/api/v2/organizations/"+child.ID, nil, nil)
}

// TestInvitation: a pending invitation is listed with its email and carries
// the token the invitee accepts with; it can be revoked, and revoking one
// that does not exist is a not-found.
func TestInvitation(t *testing.T) {
	requireInstance(t)
	email := harness.Unique("qa-invitee") + "@example.com"

	var created struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Token string `json:"token"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/invitations", map[string]string{"email": email}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, email, created.Email)
	require.NotEmpty(t, created.Token, "the inviter delivers the link, so the token is theirs to see")

	pending := harness.List[struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}](t, v2, "/api/v2/invitations?limit=200")
	var found bool
	for _, inv := range pending.Items {
		if inv.ID == created.ID {
			found = true
			require.Equal(t, email, inv.Email)
		}
	}
	require.True(t, found, "the invitation is pending")

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/invitations/"+created.ID, nil)
	require.Less(t, status, 300)
	for _, inv := range harness.List[struct {
		ID string `json:"id"`
	}](t, v2, "/api/v2/invitations?limit=200").Items {
		require.NotEqual(t, created.ID, inv.ID, "a revoked invitation is no longer pending")
	}

	v2.Refused(t, http.MethodDelete, "/api/v2/invitations/"+missing, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodPost, "/api/v2/invitations", map[string]string{}, harness.CodeInvalidArgument)
}

// TestGroupLifecycle creates a group, puts a member in it, reads both back,
// takes the member out, deletes the group, and sees it gone. A group's
// members are the group memberships naming it.
func TestGroupLifecycle(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")
	name := harness.Unique("qa-group")

	var created group
	v2.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": name}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/groups/"+created.ID, nil) })

	var found bool
	for _, g := range harness.List[group](t, v2, "/api/v2/groups?limit=200").Items {
		found = found || g.ID == created.ID
	}
	require.True(t, found)

	var gm struct {
		ID      string `json:"id"`
		GroupID string `json:"group_id"`
		UserID  string `json:"user_id"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/group-memberships", map[string]string{"group_id": created.ID, "user_id": u.ID}, &gm)
	require.Equal(t, created.ID, gm.GroupID)
	require.Equal(t, u.ID, gm.UserID)

	members := harness.List[struct {
		UserID string `json:"user_id"`
	}](t, v2, "/api/v2/group-memberships?group_id="+created.ID)
	require.Len(t, members.Items, 1)
	require.Equal(t, u.ID, members.Items[0].UserID)

	var got group
	v2.OK(t, http.MethodGet, "/api/v2/groups/"+created.ID, nil, &got)
	require.Equal(t, created, got)

	v2.OK(t, http.MethodDelete, "/api/v2/group-memberships/"+gm.ID, nil, nil)
	require.Empty(t, harness.List[any](t, v2, "/api/v2/group-memberships?group_id="+created.ID).Items)

	v2.OK(t, http.MethodDelete, "/api/v2/groups/"+created.ID, nil, nil)
	v2.Refused(t, http.MethodGet, "/api/v2/groups/"+created.ID, nil, harness.CodeNotFound)
}

// TestGroupMisuse: the group operations that must be refused.
func TestGroupMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("get absent", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/groups/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("delete absent", func(t *testing.T) {
		v2.Refused(t, http.MethodDelete, "/api/v2/groups/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("add nobody", func(t *testing.T) {
		var g group
		v2.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": harness.Unique("qa-group")}, &g)
		t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/groups/"+g.ID, nil) })
		v2.Refused(t, http.MethodPost, "/api/v2/group-memberships", map[string]string{"group_id": g.ID, "user_id": missing}, harness.CodeNotFound)
	})
	t.Run("add to absent group", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/group-memberships", map[string]string{"group_id": missing, "user_id": inst.UserID.String()}, harness.CodeNotFound)
	})
	t.Run("reserved name", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": "wheel"}, harness.CodeInvalidArgument)
	})
	t.Run("no name", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/groups", map[string]string{}, harness.CodeInvalidArgument)
	})
}

// TestGrantLifecycle gives a user a role, finds the grant in the listing and
// by id, changes the role in place, revokes it, and sees it gone.
func TestGrantLifecycle(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")
	first, second := twoRoles(t)

	var created grant
	v2.OK(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "user", "subject_id": u.ID, "role_name": first}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "user", created.Subject.Type)
	require.Equal(t, u.ID, created.Subject.ID)
	require.Equal(t, first, created.Role.Name)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/grants/"+created.ID, nil) })

	var found bool
	for _, g := range harness.List[struct {
		ID string `json:"id"`
	}](t, v2, "/api/v2/grants?limit=200").Items {
		found = found || g.ID == created.ID
	}
	require.True(t, found)

	var got grant
	v2.OK(t, http.MethodGet, "/api/v2/grants/"+created.ID, nil, &got)
	require.Equal(t, created.ID, got.ID)

	// Unlike v1, a patch keeps the grant's id.
	v2.OK(t, http.MethodPatch, "/api/v2/grants/"+created.ID, map[string]any{"role_name": second}, &got)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, second, got.Role.Name)
	v2.OK(t, http.MethodGet, "/api/v2/grants/"+created.ID, nil, &got)
	require.Equal(t, second, got.Role.Name)

	status, _ := v2.Do(t, http.MethodDelete, "/api/v2/grants/"+created.ID, nil)
	require.Less(t, status, 300)
	v2.Refused(t, http.MethodGet, "/api/v2/grants/"+created.ID, nil, harness.CodeNotFound)
	v2.Refused(t, http.MethodDelete, "/api/v2/grants/"+created.ID, nil, harness.CodeNotFound)
}

// TestGrantMisuse: the grants that must be refused.
func TestGrantMisuse(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")
	first, _ := twoRoles(t)

	t.Run("no role", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "user", "subject_id": u.ID}, harness.CodeInvalidArgument)
	})
	t.Run("unknown role", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "user", "subject_id": u.ID, "role_name": "emperor"}, harness.CodeInvalidArgument)
	})
	t.Run("nobody", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "user", "subject_id": missing, "role_name": first}, harness.CodeNotFound)
	})
	t.Run("get absent", func(t *testing.T) {
		v2.Refused(t, http.MethodGet, "/api/v2/grants/"+missing, nil, harness.CodeNotFound)
	})
	t.Run("patch absent", func(t *testing.T) {
		v2.Refused(t, http.MethodPatch, "/api/v2/grants/"+missing, map[string]any{"role_name": first}, harness.CodeNotFound)
	})
	t.Run("same role twice", func(t *testing.T) {
		var g grant
		v2.OK(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "user", "subject_id": u.ID, "role_name": first}, &g)
		t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/grants/"+g.ID, nil) })
		v2.Refused(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "user", "subject_id": u.ID, "role_name": first}, harness.CodeConflict)
	})
}

// TestGrantToGroup: a role can be held through a group, and revoking the
// grant does not touch the group.
func TestGrantToGroup(t *testing.T) {
	requireInstance(t)
	first, _ := twoRoles(t)
	var g group
	v2.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": harness.Unique("qa-group")}, &g)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/groups/"+g.ID, nil) })

	var created grant
	v2.OK(t, http.MethodPost, "/api/v2/grants", map[string]any{"subject_type": "group", "subject_id": g.ID, "role_name": first}, &created)
	require.Equal(t, "group", created.Subject.Type)
	require.Equal(t, g.ID, created.Subject.ID)

	v2.OK(t, http.MethodDelete, "/api/v2/grants/"+created.ID, nil, nil)
	v2.OK(t, http.MethodGet, "/api/v2/groups/"+g.ID, nil, nil)
}

type role struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Tier        int    `json:"tier"`
}

// TestRoleCatalogue lists the roles a grant can name and reads one; an
// unknown role is a not-found.
func TestRoleCatalogue(t *testing.T) {
	requireInstance(t)

	roles := harness.List[role](t, v2, "/api/v2/roles?limit=200")
	require.NotEmpty(t, roles.Items)
	var names []string
	for _, r := range roles.Items {
		names = append(names, r.Name)
		require.NotEmpty(t, r.DisplayName)
	}
	require.Contains(t, names, "owner")

	var owner role
	v2.OK(t, http.MethodGet, "/api/v2/roles/owner", nil, &owner)
	require.Equal(t, "owner", owner.Name)

	v2.Refused(t, http.MethodGet, "/api/v2/roles/emperor", nil, harness.CodeNotFound)
}

// twoRoles picks two assignable roles besides owner, as api/v1 does.
func twoRoles(t *testing.T) (string, string) {
	t.Helper()
	var picked []string
	for _, r := range harness.List[role](t, v2, "/api/v2/roles?limit=200").Items {
		if r.Name != "owner" && r.Tier == 3 {
			picked = append(picked, r.Name)
		}
	}
	require.GreaterOrEqual(t, len(picked), 2)
	return picked[0], picked[1]
}

type apiKey struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Reference   string `json:"reference"`
	Description string `json:"description"`
}

// TestAPIKeys: a person's key may not mint another key; an application's
// may. An owner application mints a key for a person, the key is listed on
// the account, logs in as the person, is revoked, and stops authenticating.
func TestAPIKeys(t *testing.T) {
	requireInstance(t)
	u := createUser(t, v2, "")

	own := harness.List[apiKey](t, v2, "/api/v2/api-keys?user_id="+inst.UserID.String())
	var found bool
	for _, k := range own.Items {
		found = found || k.ID == inst.APIKeyID.String()
	}
	require.True(t, found, "the key the badge came from is listed on its account")

	t.Run("a person's key may not mint", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/api-keys", map[string]string{"user_id": u.ID, "description": "qa"}, harness.CodePermissionDenied)
		v2.Refused(t, http.MethodPost, "/api/v2/api-keys", map[string]string{"user_id": inst.UserID.String()}, harness.CodePermissionDenied)
		require.Empty(t, harness.List[apiKey](t, v2, "/api/v2/api-keys?user_id="+u.ID).Items)
	})

	_, serviceKey, err := inst.ServiceAccount("QA v2 key management")
	require.NoError(t, err)
	service, err := harness.LoginV2(&harness.Instance{PlakmanURL: inst.PlakmanURL, APIKey: serviceKey})
	require.NoError(t, err)

	var minted struct {
		Key   apiKey `json:"key"`
		Token string `json:"token"`
	}
	service.OK(t, http.MethodPost, "/api/v2/api-keys", map[string]string{"user_id": u.ID, "description": "qa"}, &minted)
	require.True(t, strings.HasPrefix(minted.Token, "pcp_"), "a key is pcp_<reference>.<secret>")
	require.Equal(t, u.ID, minted.Key.UserID)
	require.Equal(t, "qa", minted.Key.Description)

	keys := harness.List[apiKey](t, v2, "/api/v2/api-keys?user_id="+u.ID)
	require.Len(t, keys.Items, 1)
	require.Equal(t, minted.Key.ID, keys.Items[0].ID)
	var got apiKey
	v2.OK(t, http.MethodGet, "/api/v2/api-keys/"+minted.Key.ID, nil, &got)
	require.Equal(t, minted.Key.Reference, got.Reference)

	as, err := harness.LoginV2(&harness.Instance{PlakmanURL: inst.PlakmanURL, APIKey: minted.Token})
	require.NoError(t, err)
	var me session
	as.OK(t, http.MethodGet, "/api/v2/auth/session", nil, &me)
	require.Equal(t, u.ID, me.UserID, "the key logs in as the user it was minted for")

	v2.OK(t, http.MethodDelete, "/api/v2/api-keys/"+minted.Key.ID, nil, nil)
	require.Empty(t, harness.List[apiKey](t, v2, "/api/v2/api-keys?user_id="+u.ID).Items)
	_, err = harness.LoginV2(&harness.Instance{PlakmanURL: inst.PlakmanURL, APIKey: minted.Token})
	require.Error(t, err, "a revoked key no longer logs in")

	v2.Refused(t, http.MethodDelete, "/api/v2/api-keys/"+minted.Key.ID, nil, harness.CodeNotFound)
	service.Refused(t, http.MethodPost, "/api/v2/api-keys", map[string]string{"user_id": missing}, harness.CodeNotFound)
}

// TestAPIKeyRotate: an application replaces the key its badge came from; with
// revoke_immediately the old key stops at once and the new one carries on.
func TestAPIKeyRotate(t *testing.T) {
	requireInstance(t)
	serviceID, serviceKey, err := inst.ServiceAccount("QA v2 rotation")
	require.NoError(t, err)
	service, err := harness.LoginV2(&harness.Instance{PlakmanURL: inst.PlakmanURL, APIKey: serviceKey})
	require.NoError(t, err)

	keys := harness.List[apiKey](t, service, "/api/v2/api-keys?user_id="+serviceID.String())
	require.Len(t, keys.Items, 1)

	var rotated struct {
		Key   apiKey `json:"key"`
		Token string `json:"token"`
	}
	service.OK(t, http.MethodPost, "/api/v2/api-keys/"+keys.Items[0].ID+"/actions/rotate",
		map[string]any{"description": "rotated", "revoke_immediately": true}, &rotated)
	require.True(t, strings.HasPrefix(rotated.Token, "pcp_"))
	require.NotEqual(t, serviceKey, rotated.Token)
	require.NotEqual(t, keys.Items[0].ID, rotated.Key.ID)

	_, err = harness.LoginV2(&harness.Instance{PlakmanURL: inst.PlakmanURL, APIKey: serviceKey})
	require.Error(t, err, "the old key stops at once")

	fresh, err := harness.LoginV2(&harness.Instance{PlakmanURL: inst.PlakmanURL, APIKey: rotated.Token})
	require.NoError(t, err)
	var me session
	fresh.OK(t, http.MethodGet, "/api/v2/auth/session", nil, &me)
	require.Equal(t, serviceID.String(), me.UserID)

	keys = harness.List[apiKey](t, fresh, "/api/v2/api-keys?user_id="+serviceID.String())
	require.Len(t, keys.Items, 1, "the revoked key is gone from the account")
	var got apiKey
	fresh.OK(t, http.MethodGet, "/api/v2/api-keys/"+keys.Items[0].ID, nil, &got)
	require.Equal(t, "rotated", got.Description)

	t.Run("not the badge's own key", func(t *testing.T) {
		v2.Refused(t, http.MethodPost, "/api/v2/api-keys/"+keys.Items[0].ID+"/actions/rotate", map[string]any{}, harness.CodePermissionDenied)
	})
}

// TestTwoFactor: the key-holder's own factors are listed, none here, and
// revoking a member's factor is refused to a badge from a key, as the v1
// reset is.
func TestTwoFactor(t *testing.T) {
	requireInstance(t)

	require.Empty(t, harness.List[any](t, v2, "/api/v2/two-factors").Items)
	v2.Refused(t, http.MethodPost, "/api/v2/two-factors/"+missing+"/actions/revoke", nil, harness.CodePermissionDenied)
	v2.Refused(t, http.MethodPost, "/api/v2/two-factors", map[string]string{"type": "totp"}, harness.CodePermissionDenied)
}

// TestMyGroups: the groups the caller is in. v2 lists group memberships by
// user instead of handing out tokens.
func TestMyGroups(t *testing.T) {
	requireInstance(t)

	var g group
	v2.OK(t, http.MethodPost, "/api/v2/groups", map[string]string{"name": harness.Unique("qa-group")}, &g)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/groups/"+g.ID, nil) })
	var gm struct {
		ID string `json:"id"`
	}
	v2.OK(t, http.MethodPost, "/api/v2/group-memberships", map[string]string{"group_id": g.ID, "user_id": inst.UserID.String()}, &gm)
	t.Cleanup(func() { v2.Do(t, http.MethodDelete, "/api/v2/group-memberships/"+gm.ID, nil) })

	mine := harness.List[struct {
		GroupID string `json:"group_id"`
	}](t, v2, "/api/v2/group-memberships?user_id="+inst.UserID.String())
	var found bool
	for _, m := range mine.Items {
		found = found || m.GroupID == g.ID
	}
	require.True(t, found, "the caller's memberships name the group")
}
