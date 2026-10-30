// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

// Package harness connects the API scenarios to a standing plakman instance
// (see `make instance`), the same one for both API versions: the v1
// scenarios reach it through pcpctl, the v2 ones over HTTP, and both hold the
// same API key, minted here for the seeded administrator.
//
// What is asserted lives in api/v1 and api/v2. This package only knows how to
// get there.
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	api "github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/client"
	"github.com/PlakarKorp/plakman/model/organization"
	"github.com/google/uuid"
)

// The seeded administrator, as cmd/enroll created it.
const (
	AdminEmail    = "admin@example.com"
	AdminPassword = "foobar"
	RootOrgName   = "Root organization"
)

// Instance is one connection to a plakman under test, as the seeded
// administrator, with an API key minted for the scenarios.
type Instance struct {
	PlakmanURL string
	ParentURL  string

	// OrgID is the root organization the administrator's key is issued in.
	OrgID uuid.UUID
	// UserID is the administrator.
	UserID uuid.UUID
	// APIKey is a key minted for the administrator, as "pcp_<ref>.<secret>".
	// pcpctl logs in with it; the v2 client trades it for a badge.
	APIKey string
	// APIKeyID addresses the key, for the scenarios that manage keys.
	APIKeyID uuid.UUID

	// V1 is an owner-badged v1 client, for the setup a scenario cannot do
	// through the surface under test.
	V1 *client.Client
}

// Env reads a variable with a default.
func Env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return def
}

// Connect logs in as the seeded administrator and mints the API key the
// scenarios use. It follows cmd/seed: the root organization's id comes from
// the parent, which is where plakman got it.
func Connect() (*Instance, error) {
	inst := &Instance{
		PlakmanURL: Env("PLAKMAN_URL", "http://localhost:18080"),
		ParentURL:  Env("PLAKMAN_PARENT", "http://localhost:18081"),
	}

	parent := client.NewServer(inst.ParentURL)
	plakman := client.NewServer(inst.PlakmanURL)

	// core-api seeds this user in dev mode.
	var login api.LoginResponse
	if err := parent.Client().Endpoint("/api/v1/auth/login/password").Post(struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{"test@plakar.io", "foobar"}, &login); err != nil {
		return nil, fmt.Errorf("log in to parent: %w", err)
	}
	var orgs []api.OrganizationResponse
	if err := parent.Client(client.WithToken(login.Token)).Endpoint("/api/v1/account/organizations").Get(&orgs); err != nil {
		return nil, fmt.Errorf("read organizations from parent: %w", err)
	}
	for _, o := range orgs {
		if o.Name == RootOrgName {
			inst.OrgID = o.Id
		}
	}
	if inst.OrgID == uuid.Nil {
		return nil, fmt.Errorf("parent has no %q; enroll first", RootOrgName)
	}

	flow := client.NewLoginFlow(plakman.Client())
	if err := flow.Login(api.LoginPasswordRequest{
		Account:  organization.IDPrefix(inst.OrgID) + "/" + AdminEmail,
		Password: AdminPassword,
	}); err != nil {
		return nil, fmt.Errorf("log in as %s: %w", AdminEmail, err)
	}
	for !flow.State().IsFinal() {
		time.Sleep(10 * time.Millisecond)
	}
	if flow.State().Token == "" {
		return nil, fmt.Errorf("login as %s produced no token", AdminEmail)
	}

	var badge api.TokenResponse
	if err := plakman.Client(client.WithToken(flow.State().Token)).
		Endpoint("/api/v1/auth/badge").
		Post(api.BadgeRequest{OrganizationId: inst.OrgID, Role: "owner"}, &badge); err != nil {
		return nil, fmt.Errorf("owner badge: %w", err)
	}
	inst.V1 = plakman.Client(client.WithToken(badge.Token))

	var me api.AccountMeResponse
	if err := inst.V1.Endpoint("/api/v1/account/me").Get(&me); err != nil {
		return nil, fmt.Errorf("read account: %w", err)
	}
	inst.UserID = me.UserId

	// One key per run, so a leaked key dies with the instance.
	var key api.AddAPIKeyResponse
	if err := inst.V1.Endpoint("/api/v1/users/"+inst.UserID.String()+"/api-keys").
		Post(api.AddAPIKeyRequest{Description: "QA api scenarios"}, &key); err != nil {
		return nil, fmt.Errorf("mint api key: %w", err)
	}
	inst.APIKey = key.ApiKey
	inst.APIKeyID = key.Id

	return inst, nil
}

// ModuleRoot walks up from the working directory to the module's go.mod, so
// a test can find what the rig built into bin/ whatever package it runs from.
func ModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// Unique returns a name no other run or scenario used, so scenarios can share
// an instance and still find only what they created.
func Unique(prefix string) string {
	return prefix + "-" + uuid.NewString()[:8]
}

// ServiceAccount provisions an application account holding owner in the root
// organization and returns its id and API key. An application's key may mint
// keys where a person's may not, so the key-management scenarios need one;
// the account is created through the owner badge, which is the interactive
// credential that step requires.
func (inst *Instance) ServiceAccount(description string) (uuid.UUID, string, error) {
	org := inst.OrgID.String()

	var invited struct {
		Accepted struct {
			UserID uuid.UUID `json:"user_id"`
		} `json:"accepted"`
	}
	if err := inst.V1.Endpoint("/api/v1/account/organizations/"+org+"/invitations").Post(map[string]any{
		"name":        Unique("qa-service"),
		"is_service":  true,
		"auto_accept": true,
	}, &invited); err != nil {
		return uuid.Nil, "", fmt.Errorf("create service account: %w", err)
	}
	userID := invited.Accepted.UserID
	if userID == uuid.Nil {
		return uuid.Nil, "", fmt.Errorf("create service account: no user id in the answer")
	}

	if err := inst.V1.Endpoint("/api/v1/account/organizations/"+org+"/access").Post(map[string]any{
		"subject_type": "user",
		"subject_id":   userID,
		"role_name":    "owner",
	}, nil); err != nil {
		return uuid.Nil, "", fmt.Errorf("grant owner to service account: %w", err)
	}

	var key api.AddAPIKeyResponse
	if err := inst.V1.Endpoint("/api/v1/users/"+userID.String()+"/api-keys").
		Post(api.AddAPIKeyRequest{Description: description}, &key); err != nil {
		return uuid.Nil, "", fmt.Errorf("mint service account key: %w", err)
	}
	return userID, key.ApiKey, nil
}
