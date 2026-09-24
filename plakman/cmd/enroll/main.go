// enroll drives a fresh plakman instance through enrollment against core-api. It reimplements cmd/bootstrap's Enroll() rather than calling it,
// because that command hardcodes localhost:8080 and localhost:8081 -- a rig has
// to be able to point at whatever ports it actually bound.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/client"
	"github.com/PlakarKorp/plakman/credentials"
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

var (
	confirmLink = regexp.MustCompile(`https?://\S+/v1/auth/confirm/[0-9a-f-]{36}`)
	codeOnPage  = regexp.MustCompile(`class="completion-code">\s*([A-Z0-9]{4}-[A-Z0-9]{4})\s*<`)
)

// completionCode does what the person signing in would: open the login email,
// confirm, and read the code off the page core-api lands on.
func completionCode(mailpit, to string) (string, error) {
	var link string
	for deadline := time.Now().Add(30 * time.Second); link == ""; time.Sleep(time.Second) {
		if time.Now().After(deadline) {
			return "", errors.New("no login email for " + to)
		}
		var found struct {
			Messages []struct{ ID string }
		}
		if err := getJSON(mailpit+"/api/v1/search?query="+url.QueryEscape("to:"+to), &found); err != nil {
			return "", err
		}
		if len(found.Messages) == 0 {
			continue
		}
		var msg struct{ Text string }
		if err := getJSON(mailpit+"/api/v1/message/"+found.Messages[0].ID, &msg); err != nil {
			return "", err
		}
		if link = confirmLink.FindString(msg.Text); link == "" {
			return "", errors.New("no confirm link in the login email")
		}
	}

	// The confirm page is a form: the POST spends the link and redirects to the
	// callback, which renders the code.
	resp, err := http.Post(link, "", nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	page, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	m := codeOnPage.FindSubmatch(page)
	if m == nil {
		return "", fmt.Errorf("no completion code on the sign-in page (status %d)", resp.StatusCode)
	}
	return string(m[1]), nil
}

func getJSON(u string, into any) error {
	resp, err := http.Get(u)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func main() {
	mailpitURL := flag.String("mailpit", "http://localhost:18025", "mailpit URL, where core-api's login email lands")
	plakmanURL := flag.String("plakman", "http://localhost:18080", "plakman URL")
	orgName := flag.String("org", "Root organization", "root organization name")
	adminEmail := flag.String("email", "admin@example.com", "admin email")
	adminPass := flag.String("password", "foobar", "admin password")
	flag.Parse()

	plakman := client.NewServer(*plakmanURL)
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

	// Sign in the way the UI does: plakman relays the login to the parent, and
	// the poll releases the token only with the completion code.
	var loginRes contract.LoginResponse
	if err := plakman.Client().Endpoint("/api/v1/bootstrap/login").
		Post(&contract.BootstrapLoginRequest{Email: "test@plakar.io"}, &loginRes); err != nil {
		fail("POST /bootstrap/login", err)
	}
	code, err := completionCode(*mailpitURL, "test@plakar.io")
	if err != nil {
		fail("read completion code", err)
	}
	// Not contract.BootstrapLoginPollRequest: the plakman this module pins
	// predates the completion code.
	poll := struct {
		PollId         string `json:"poll_id"`
		CompletionCode string `json:"completion_code"`
	}{loginRes.PollId, code}
	var tokenRes contract.TokenResponse
	if err := plakman.Client().Endpoint("/api/v1/bootstrap/login/poll").Post(&poll, &tokenRes); err != nil {
		fail("POST /bootstrap/login/poll", err)
	}
	ok("bootstrap login with completion code")

	bc := plakman.Client(client.WithBootstrapToken(tokenRes.Token))

	var orgs []contract.OrganizationResponse
	if err := bc.Endpoint("/api/v1/bootstrap/organizations").Get(&orgs); err != nil {
		fail("GET /bootstrap/organizations", err)
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
		if err := bc.Endpoint("/api/v1/bootstrap/organizations").Post(
			&contract.BootstrapOrganizationCreateRequest{Name: *orgName, Info: map[string]string{}}, &created); err != nil {
			fail("POST /bootstrap/organizations", err)
		}
		orgID = created.Id
	}
	ok("parent organization", orgID)

	// plakman re-presents the bootstrap token to the parent, pulls a licensing
	// challenge, signs it, and mints the root org + admin.
	var enrolled contract.TokenResponse
	err = bc.Endpoint("/api/v1/bootstrap/enroll").
		Post(&contract.BootstrapEnrollRequest{
			OrganizationId: orgID,
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

	fmt.Println("\nPASS  plakman enrolled against core-api, no human, no team secrets")
}
