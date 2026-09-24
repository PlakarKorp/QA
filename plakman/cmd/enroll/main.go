// enroll drives a fresh plakman instance through enrollment against the mock
// parent. It reimplements cmd/bootstrap's Enroll() rather than calling it,
// because that command hardcodes localhost:8080 and localhost:8081 -- a rig has
// to be able to point at whatever ports it actually bound.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/client"
	"github.com/PlakarKorp/plakman/credentials"
	"github.com/PlakarKorp/plakman/model/organization"
	"github.com/PlakarKorp/plakman/pki"
	"github.com/google/uuid"
)

func asHTTP(err error, target **client.HttpError) bool {
	return errors.As(err, target)
}

func fail(step string, err error) {
	fmt.Printf("FAIL  %s: %v\n", step, err)
	os.Exit(1)
}

func ok(step string, args ...any) {
	fmt.Printf("ok    %s", step)
	if len(args) > 0 {
		fmt.Printf(" (%v)", args[0])
	}
	fmt.Println()
}

func main() {
	parentURL := flag.String("parent", "http://localhost:18081", "mock parent URL")
	plakmanURL := flag.String("plakman", "http://localhost:18080", "plakman URL")
	orgName := flag.String("org", "Root organization", "root organization name")
	adminEmail := flag.String("email", "admin@example.com", "admin email")
	adminPass := flag.String("password", "foobar", "admin password")
	flag.Parse()

	parent := client.NewServer(*parentURL)
	plakman := client.NewServer(*plakmanURL)
	parentJWT := credentials.JWT{Pki: client.NewPki(parent.Client(), pki.NewPkiMemory())}
	plakmanJWT := credentials.JWT{Pki: client.NewPki(plakman.Client(), pki.NewPkiMemory())}

	// Wait for plakman to answer at all: the container builds and migrates on
	// first boot, so "connection refused" here is normal for a while.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		resp, err := plakman.Client().Endpoint("/health").Do("GET", nil)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code == 200 {
				ok("plakman is up")
				break
			}
			err = fmt.Errorf("health returned %d", code)
		}
		if time.Now().After(deadline) {
			fail("waiting for plakman", err)
		}
		time.Sleep(2 * time.Second)
	}

	var loginRes contract.LoginResponse
	if err := parent.Client().Endpoint("/api/v1/auth/login/password").Post(struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{"test@plakar.io", "foobar"}, &loginRes); err != nil {
		fail("parent login", err)
	}
	ok("parent login")

	pc := parent.Client(client.WithToken(loginRes.Token))

	var orgs []contract.OrganizationResponse
	if err := pc.Endpoint("/api/v1/account/organizations").Get(&orgs); err != nil {
		fail("list parent organizations", err)
	}
	var orgID uuid.UUID
	for _, o := range orgs {
		if o.Name == *orgName {
			orgID = o.Id
			break
		}
	}
	if orgID == uuid.Nil {
		var created contract.OrganizationResponse
		if err := pc.Endpoint("/api/v1/account/organizations").Post(contract.OrganizationCreateRequest{
			Type: organization.OrganizationTypeEnterprise,
			Name: *orgName,
			Info: map[string]string{},
		}, &created); err != nil {
			fail("create parent organization", err)
		}
		orgID = created.Id
	}
	ok("parent organization", orgID)

	var badgeRes contract.TokenResponse
	if err := pc.Endpoint("/api/v1/auth/badge").Post(
		contract.BadgeRequest{OrganizationId: orgID, Role: "owner"}, &badgeRes); err != nil {
		fail("request badge", err)
	}
	var badge credentials.Badge
	if err := parentJWT.ParseSignedString(badgeRes.Token, &badge); err != nil {
		fail("verify badge", err)
	}
	ok("badge issued and verified", badge.Organization.Id)

	// The badge is the bootstrap token: plakman re-presents it to the parent,
	// pulls a licensing challenge, signs it, and mints the root org + admin.
	var enrolled contract.TokenResponse
	err := plakman.Client(client.WithBootstrapToken(badgeRes.Token)).
		Endpoint("/api/v1/bootstrap/enroll").
		Post(&contract.BootstrapEnrollRequest{
			OrganizationId: badge.Organization.Id,
			Name:           "Admin",
			Email:          *adminEmail,
			Password:       *adminPass,
		}, &enrolled)
	if err != nil {
		var httpErr *client.HttpError
		if ok2 := asHTTP(err, &httpErr); ok2 && httpErr.Response.StatusCode == 409 {
			fmt.Println("ok    already enrolled (409), nothing to do")
			return
		}
		fail("POST /bootstrap/enroll", err)
	}
	ok("plakman enrolled")

	var adminBadge credentials.Badge
	if err := plakmanJWT.ParseSignedString(enrolled.Token, &adminBadge); err != nil {
		fail("verify plakman admin badge", err)
	}
	ok("admin badge verified against plakman PKI", adminBadge.Organization.Id)

	var me contract.AccountMeResponse
	if err := plakman.Client(client.WithToken(enrolled.Token)).
		Endpoint("/api/v1/account/me").Get(&me); err != nil {
		fail("GET /account/me", err)
	}
	ok("admin account live", me.Email)

	fmt.Println("\nPASS  plakman enrolled against a mock parent, no human, no team secrets")
}
