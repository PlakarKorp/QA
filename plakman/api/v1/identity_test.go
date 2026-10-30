// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv1

import (
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

type member struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsService bool   `json:"is_service"`
}

type group struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
	Members     []struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	} `json:"members"`
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

type role struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Category    string `json:"category"`
}

// invited is what an accepted invitation answers with.
type invited struct {
	Accepted struct {
		UserID         string `json:"user_id"`
		Email          string `json:"email"`
		OrganizationID string `json:"organization_id"`
	} `json:"accepted"`
	Invitation struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"invitation"`
	IsService bool `json:"is_service"`
}

// inviteUser creates a user in the organization in scope with the membership
// made at once, which is how an administrator provisions an account by hand.
// The user is global and outlives the membership, so nothing removes it: the
// email is unique per test and the directory grows by one.
func inviteUser(t *testing.T, p *harness.Pcpctl, extra ...string) user {
	t.Helper()
	name := harness.Unique("qa-user")
	email := name + "@example.com"
	var inv invited
	p.JSON(t, &inv, append([]string{"invitation", "create", name, "--email", email, "--auto-accept"}, extra...)...)
	require.NotEmpty(t, inv.Accepted.UserID, "an auto-accepted invitation names the user it made")

	var u user
	p.JSON(t, &u, "user", "get", inv.Accepted.UserID)
	require.Equal(t, inv.Accepted.Email, u.Email)
	t.Cleanup(func() { p.Run(t, "member", "remove", u.ID, "--yes") })
	return u
}

func memberIDs(t *testing.T, p *harness.Pcpctl) []string {
	t.Helper()
	var members page[member]
	p.JSON(t, &members, "member", "list")
	ids := make([]string, len(members.Items))
	for i, m := range members.Items {
		ids[i] = m.UserID
	}
	return ids
}

// TestWhoami: the key answers for the administrator it was minted for, in the
// organization it was issued in.
func TestWhoami(t *testing.T) {
	requireInstance(t)

	var me struct {
		UserID         string `json:"user_id"`
		OrganizationID string `json:"organization_id"`
		Email          string `json:"email"`
		Account        string `json:"account"`
	}
	pcpctl.JSON(t, &me, "whoami")
	require.Equal(t, inst.UserID.String(), me.UserID)
	require.Equal(t, inst.OrgID.String(), me.OrganizationID)
	require.Equal(t, harness.AdminEmail, me.Email)
	require.Equal(t, harness.AdminEmail, me.Account, "at the deployment root the account is the bare email")
}

// TestAuthRejectsBadKey: a key that is not one, and no key at all, are
// refused as unauthorized before anything is attempted.
func TestAuthRejectsBadKey(t *testing.T) {
	requireInstance(t)

	bad := *pcpctl
	bad.APIKey = "pcp_ak_nope.nope"
	bad.Fails(t, harness.ExitUnauthorized, "whoami")

	none := *pcpctl
	none.APIKey = ""
	none.Fails(t, harness.ExitUnauthorized, "whoami")
}

// TestUserDirectory: a provisioned user is in the directory, readable by id
// and by email, and a lookup of someone absent says so.
func TestUserDirectory(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)
	require.False(t, u.IsService)
	require.True(t, strings.HasSuffix(u.Email, "@example.com"), "a person keeps the email they were invited at")

	var byEmail user
	pcpctl.JSON(t, &byEmail, "user", "get", u.Email)
	require.Equal(t, u, byEmail, "get resolves an email")

	var users page[user]
	pcpctl.JSON(t, &users, "user", "list")
	var found bool
	for _, x := range users.Items {
		found = found || x.ID == u.ID
	}
	require.True(t, found, "the directory lists the user")

	pcpctl.Fails(t, harness.ExitNotFound, "user", "get", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "user", "get", "nobody-"+harness.Unique("x")+"@example.com")
}

// TestUserRename: a name is the person's own, so an API key -- a delegation,
// not the person -- may not change it, theirs or anyone else's. The refusal
// is an unauthorized, and the name stays.
func TestUserRename(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)

	res := pcpctl.Fails(t, harness.ExitUnauthorized, "user", "rename", u.ID, "Renamed Person")
	require.Contains(t, res.Stderr, "Forbidden")
	pcpctl.Fails(t, harness.ExitUnauthorized, "user", "rename", inst.UserID.String(), "Renamed Admin")

	var got user
	pcpctl.JSON(t, &got, "user", "get", u.ID)
	require.Equal(t, u.Name, got.Name)
}

// TestServiceUser: an application account is created as such, gets a
// synthetic address of its own rather than a person's, and is marked service
// in the directory and the member list.
func TestServiceUser(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl, "--service")
	require.True(t, u.IsService)
	require.True(t, strings.HasSuffix(u.Email, "@service.internal"), "an application's address is minted, not the one given")

	var members page[member]
	pcpctl.JSON(t, &members, "member", "list")
	for _, m := range members.Items {
		if m.UserID == u.ID {
			require.True(t, m.IsService)
			return
		}
	}
	t.Fatalf("service user %s is not a member", u.ID)
}

// TestMembership adds a user to a sub-organization, lists them there,
// removes them, and refuses the removals that make no sense.
func TestMembership(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	require.NotContains(t, memberIDs(t, in), u.ID)

	var added member
	in.JSON(t, &added, "member", "add", u.ID)
	require.Equal(t, u.ID, added.UserID)
	require.Equal(t, u.Email, added.Email)
	require.Contains(t, memberIDs(t, in), u.ID)

	t.Run("add twice", func(t *testing.T) {
		in.Fails(t, harness.ExitConflict, "member", "add", u.ID)
	})
	t.Run("add nobody", func(t *testing.T) {
		in.Fails(t, harness.ExitNotFound, "member", "add", missing)
	})
	t.Run("remove without --yes", func(t *testing.T) {
		in.Fails(t, harness.ExitError, "member", "remove", u.ID)
		require.Contains(t, memberIDs(t, in), u.ID)
	})
	t.Run("remove oneself", func(t *testing.T) {
		// In the organization the key acts in; the caller is not a member of
		// the child at all, which is a different refusal.
		pcpctl.Fails(t, harness.ExitConflict, "member", "remove", inst.UserID.String(), "--yes")
		in.Fails(t, harness.ExitNotFound, "member", "remove", inst.UserID.String(), "--yes")
	})

	in.OK(t, "member", "remove", u.ID, "--yes")
	require.NotContains(t, memberIDs(t, in), u.ID)
	pcpctl.OK(t, "user", "get", u.ID)

	t.Run("remove twice", func(t *testing.T) {
		in.Fails(t, harness.ExitNotFound, "member", "remove", u.ID, "--yes")
	})
}

// TestOrganizationDeleteWithMembers: an organization that still has other
// members is not deleted; once they are gone it can be.
func TestOrganizationDeleteWithMembers(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)
	in.OK(t, "member", "add", u.ID)

	pcpctl.Fails(t, harness.ExitConflict, "organization", "delete", child.ID, "--yes")
	pcpctl.OK(t, "organization", "get", child.ID)

	in.OK(t, "member", "remove", u.ID, "--yes")
	pcpctl.OK(t, "organization", "delete", child.ID, "--yes")
}

// TestInvitation: a pending invitation is listed with its email and can be
// revoked; revoking one that does not exist is a not-found.
func TestInvitation(t *testing.T) {
	requireInstance(t)
	name := harness.Unique("qa-invitee")
	email := name + "@example.com"

	var created invited
	pcpctl.JSON(t, &created, "invitation", "create", name, "--email", email)
	require.NotEmpty(t, created.Invitation.ID)
	require.Equal(t, email, created.Invitation.Email)
	require.Empty(t, created.Accepted.UserID, "without --auto-accept nobody is a member yet")

	var pending []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	pcpctl.JSON(t, &pending, "invitation", "list")
	var found bool
	for _, inv := range pending {
		if inv.ID == created.Invitation.ID {
			found = true
			require.Equal(t, email, inv.Email)
		}
	}
	require.True(t, found, "the invitation is pending")

	pcpctl.Fails(t, harness.ExitError, "invitation", "revoke", created.Invitation.ID)
	pcpctl.OK(t, "invitation", "revoke", created.Invitation.ID, "--yes")
	pcpctl.JSON(t, &pending, "invitation", "list")
	for _, inv := range pending {
		require.NotEqual(t, created.Invitation.ID, inv.ID, "a revoked invitation is no longer pending")
	}

	pcpctl.Fails(t, harness.ExitNotFound, "invitation", "revoke", missing, "--yes")
	pcpctl.Fails(t, harness.ExitUsage, "invitation", "create")
}

// TestGroupLifecycle creates a group, puts a member in it, reads the group
// back by id and by name, takes the member out, deletes the group, and sees
// it gone.
func TestGroupLifecycle(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)
	name := harness.Unique("qa-group")

	var created group
	pcpctl.JSON(t, &created, "group", "create", name)
	require.NotEmpty(t, created.ID)
	require.Equal(t, name, created.Name)
	t.Cleanup(func() { pcpctl.Run(t, "group", "delete", created.ID, "--yes") })

	var groups []group
	pcpctl.JSON(t, &groups, "group", "list")
	var found bool
	for _, g := range groups {
		found = found || g.ID == created.ID
	}
	require.True(t, found)

	pcpctl.OK(t, "group", "add", created.ID, u.ID)
	var got group
	pcpctl.JSON(t, &got, "group", "get", name)
	require.Equal(t, created.ID, got.ID, "get resolves a name")
	require.Len(t, got.Members, 1)
	require.Equal(t, u.ID, got.Members[0].UserID)

	pcpctl.OK(t, "group", "remove", created.ID, u.ID)
	pcpctl.JSON(t, &got, "group", "get", created.ID)
	require.Empty(t, got.Members)

	pcpctl.OK(t, "group", "delete", created.ID, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "group", "get", created.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "group", "get", name)
}

// TestGroupMisuse: the group operations that must be refused.
func TestGroupMisuse(t *testing.T) {
	requireInstance(t)

	t.Run("get absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "group", "get", missing)
	})
	t.Run("delete absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "group", "delete", missing, "--yes")
	})
	t.Run("delete without --yes", func(t *testing.T) {
		var g group
		pcpctl.JSON(t, &g, "group", "create", harness.Unique("qa-group"))
		t.Cleanup(func() { pcpctl.Run(t, "group", "delete", g.ID, "--yes") })
		pcpctl.Fails(t, harness.ExitError, "group", "delete", g.ID)
		pcpctl.OK(t, "group", "get", g.ID)
	})
	t.Run("add nobody", func(t *testing.T) {
		var g group
		pcpctl.JSON(t, &g, "group", "create", harness.Unique("qa-group"))
		t.Cleanup(func() { pcpctl.Run(t, "group", "delete", g.ID, "--yes") })
		pcpctl.Fails(t, harness.ExitNotFound, "group", "add", g.ID, missing)
	})
	t.Run("add to absent group", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "group", "add", missing, inst.UserID.String())
	})
	t.Run("reserved name", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "group", "create", "wheel")
	})
}

// TestGrantLifecycle gives a user a role, finds the grant in the listing and
// by id, changes the role, revokes it, and sees it gone.
func TestGrantLifecycle(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)
	first, second := twoRoles(t)

	var created grant
	pcpctl.JSON(t, &created, "grant", "create", "--user", u.Email, "--role", first)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "user", created.Subject.Type)
	require.Equal(t, u.ID, created.Subject.ID)
	require.Equal(t, first, created.Role.Name)
	t.Cleanup(func() { pcpctl.Run(t, "grant", "revoke", created.ID, "--yes") })

	var grants page[grant]
	pcpctl.JSON(t, &grants, "grant", "list")
	var found bool
	for _, g := range grants.Items {
		found = found || g.ID == created.ID
	}
	require.True(t, found)

	var got grant
	pcpctl.JSON(t, &got, "grant", "get", created.ID)
	require.Equal(t, created.ID, got.ID)

	// An update replaces the grant: the answer is a new grant, and the old id
	// names nothing afterwards.
	var updated grant
	pcpctl.JSON(t, &updated, "grant", "update", created.ID, "--role", second)
	require.Equal(t, second, updated.Role.Name)
	require.Equal(t, u.ID, updated.Subject.ID)
	require.NotEqual(t, created.ID, updated.ID, "an updated grant gets an id of its own")
	t.Cleanup(func() { pcpctl.Run(t, "grant", "revoke", updated.ID, "--yes") })
	pcpctl.Fails(t, harness.ExitNotFound, "grant", "get", created.ID)
	pcpctl.JSON(t, &got, "grant", "get", updated.ID)
	require.Equal(t, second, got.Role.Name)

	pcpctl.Fails(t, harness.ExitError, "grant", "revoke", updated.ID)
	pcpctl.OK(t, "grant", "revoke", updated.ID, "--yes")
	pcpctl.Fails(t, harness.ExitNotFound, "grant", "get", updated.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "grant", "revoke", updated.ID, "--yes")
}

// TestGrantMisuse: the grants that must be refused.
func TestGrantMisuse(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)
	first, _ := twoRoles(t)

	t.Run("no role", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "grant", "create", "--user", u.Email)
	})
	t.Run("unknown role", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "grant", "create", "--user", u.Email, "--role", "emperor")
	})
	t.Run("nobody", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "grant", "create", "--user", missing, "--role", first)
	})
	t.Run("get absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "grant", "get", missing)
	})
	t.Run("update absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "grant", "update", missing, "--role", first)
	})
	t.Run("same role twice", func(t *testing.T) {
		var g grant
		pcpctl.JSON(t, &g, "grant", "create", "--user", u.Email, "--role", first)
		t.Cleanup(func() { pcpctl.Run(t, "grant", "revoke", g.ID, "--yes") })
		pcpctl.Fails(t, harness.ExitConflict, "grant", "create", "--user", u.Email, "--role", first)
	})
}

// TestGrantToGroup: a role can be held through a group, and revoking the
// grant does not touch the group.
func TestGrantToGroup(t *testing.T) {
	requireInstance(t)
	first, _ := twoRoles(t)
	var g group
	pcpctl.JSON(t, &g, "group", "create", harness.Unique("qa-group"))
	t.Cleanup(func() { pcpctl.Run(t, "group", "delete", g.ID, "--yes") })

	var created grant
	pcpctl.JSON(t, &created, "grant", "create", "--group", g.Name, "--role", first)
	require.Equal(t, "group", created.Subject.Type)
	require.Equal(t, g.ID, created.Subject.ID)

	pcpctl.OK(t, "grant", "revoke", created.ID, "--yes")
	pcpctl.OK(t, "group", "get", g.ID)
}

// TestRoleCatalogue lists the roles a grant can name and reads one; an
// unknown role is a not-found.
func TestRoleCatalogue(t *testing.T) {
	requireInstance(t)

	var roles []role
	pcpctl.JSON(t, &roles, "role", "list")
	require.NotEmpty(t, roles)
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
		require.NotEmpty(t, r.DisplayName, "%s has a display name", r.Name)
	}
	require.Contains(t, names, "owner")

	var owner role
	pcpctl.JSON(t, &owner, "role", "get", "owner")
	require.Equal(t, "owner", owner.Name)

	pcpctl.Fails(t, harness.ExitNotFound, "role", "get", "emperor")
}

// twoRoles picks two assignable roles from the catalogue, neither of them
// owner, so a grant scenario never changes who administers the instance.
func twoRoles(t *testing.T) (string, string) {
	t.Helper()
	var roles []role
	pcpctl.JSON(t, &roles, "role", "list")
	var picked []string
	for _, r := range roles {
		if r.Name != "owner" {
			picked = append(picked, r.Name)
		}
	}
	require.GreaterOrEqual(t, len(picked), 2, "the catalogue offers at least two roles besides owner")
	return picked[0], picked[1]
}

// TestAPIKeys: a person's key may not mint another key -- revoking it would
// not end the access it granted -- so pcpctl under such a key can list and
// revoke keys but not add one. An application's key is the whole account and
// may, so an owner application mints a key for a person, logs in with it,
// revokes it, and sees it stop authenticating.
func TestAPIKeys(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)

	var keys []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	pcpctl.JSON(t, &keys, "user", "api-keys", "list", inst.UserID.String())
	var found bool
	for _, k := range keys {
		found = found || k.ID == inst.APIKeyID.String()
	}
	require.True(t, found, "the key pcpctl holds is listed on its account")

	t.Run("a person's key may not mint", func(t *testing.T) {
		res := pcpctl.Fails(t, harness.ExitUnauthorized, "user", "api-keys", "add", u.ID, "--description", "qa")
		require.Contains(t, res.Stderr, "Forbidden")
		pcpctl.Fails(t, harness.ExitUnauthorized, "user", "api-keys", "add", inst.UserID.String())
		pcpctl.JSON(t, &keys, "user", "api-keys", "list", u.ID)
		require.Empty(t, keys)
	})

	_, serviceKey, err := inst.ServiceAccount("QA key management")
	require.NoError(t, err)
	service := *pcpctl
	service.APIKey = serviceKey

	// The minted key is printed bare, whatever --output says, so a script can
	// capture it and nothing else.
	minted := strings.TrimSpace(service.OK(t, "user", "api-keys", "add", u.ID, "--description", "qa"))
	require.True(t, strings.HasPrefix(minted, "pcp_"), "a key is pcp_<reference>.<secret>: %q", minted)
	require.NotContains(t, minted, "\n")

	pcpctl.JSON(t, &keys, "user", "api-keys", "list", u.ID)
	require.Len(t, keys, 1)
	require.Equal(t, "qa", keys[0].Description)
	keyID := keys[0].ID

	as := *pcpctl
	as.APIKey = minted
	var me struct {
		UserID string `json:"user_id"`
	}
	as.JSON(t, &me, "whoami")
	require.Equal(t, u.ID, me.UserID, "the key logs in as the user it was minted for")

	pcpctl.Fails(t, harness.ExitError, "user", "api-keys", "revoke", u.ID, keyID)
	pcpctl.OK(t, "user", "api-keys", "revoke", u.ID, keyID, "--yes")
	pcpctl.JSON(t, &keys, "user", "api-keys", "list", u.ID)
	require.Empty(t, keys)
	as.Fails(t, harness.ExitUnauthorized, "whoami")

	pcpctl.Fails(t, harness.ExitNotFound, "user", "api-keys", "revoke", u.ID, keyID, "--yes")
	service.Fails(t, harness.ExitNotFound, "user", "api-keys", "add", missing)
}

// TestAPIKeyRotate: an application replaces its own key with a fresh one; with
// --now the old key stops at once and the new one carries on.
func TestAPIKeyRotate(t *testing.T) {
	requireInstance(t)
	serviceID, serviceKey, err := inst.ServiceAccount("QA rotation")
	require.NoError(t, err)
	service := *pcpctl
	service.APIKey = serviceKey

	rotated := strings.TrimSpace(service.OK(t, "user", "api-keys", "rotate", "--now", "--description", "rotated"))
	require.True(t, strings.HasPrefix(rotated, "pcp_"), "the new key is printed bare: %q", rotated)
	require.NotEqual(t, serviceKey, rotated)

	service.Fails(t, harness.ExitUnauthorized, "whoami")

	fresh := *pcpctl
	fresh.APIKey = rotated
	var me struct {
		UserID string `json:"user_id"`
	}
	fresh.JSON(t, &me, "whoami")
	require.Equal(t, serviceID.String(), me.UserID)

	var keys []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	fresh.JSON(t, &keys, "user", "api-keys", "list", serviceID.String())
	require.Len(t, keys, 1, "the revoked key is gone from the account")
	require.Equal(t, "rotated", keys[0].Description)
}

// TestTwoFactor: the key-holder's own factors are listed, none here, and the
// changes a second factor needs an interactive credential for are refused to
// a key.
func TestTwoFactor(t *testing.T) {
	requireInstance(t)
	u := inviteUser(t, pcpctl)

	var methods []any
	pcpctl.JSON(t, &methods, "2fa", "list")
	require.Empty(t, methods)

	pcpctl.Fails(t, harness.ExitUnauthorized, "2fa", "reset", u.ID, "--yes")
	pcpctl.Fails(t, harness.ExitError, "2fa", "reset", u.ID)
}

// TestMyGroups: the caller's own group tokens include the built-in ones.
func TestMyGroups(t *testing.T) {
	requireInstance(t)

	var groups []string
	pcpctl.JSON(t, &groups, "my-groups")
	require.NotEmpty(t, groups)
}
