// Package scenarios drives a standing plakman the way a client does, covering
// what plakman's own suite does not reach. `make instance` stands one up.
//
// Two kinds of assertion live here. Reads check that an endpoint answers at
// all -- a surprising amount of the API had never been called by anything.
// Misuse checks the shape of a refusal: a bad request must be refused with a
// 4xx, and a 5xx is a bug even when the request deserved to fail, because it
// means the error path was never written.
package scenarios

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	api "github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/client"
	"github.com/PlakarKorp/plakman/model/organization"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var (
	plakmanURL = env("PLAKMAN_URL", "http://localhost:18080")
	parentURL  = env("PLAKMAN_PARENT", "http://localhost:18081")

	owner *client.Client
	orgID uuid.UUID
	ready bool
)

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return def
}

func TestMain(m *testing.M) {
	flag.Parse()
	if err := connect(); err != nil {
		fmt.Fprintln(os.Stderr, "SKIP scenarios: no instance:", err)
	} else {
		ready = true
	}
	os.Exit(m.Run())
}

// connect logs in as the seeded admin and takes an owner badge. The root
// organization's id comes from the parent, which is where plakman got it.
func connect() error {
	parent := client.NewServer(parentURL)
	plakman := client.NewServer(plakmanURL)

	var orgs []api.OrganizationResponse
	if err := parent.Client().Endpoint("/api/v1/account/organizations").Get(&orgs); err != nil {
		return err
	}
	if len(orgs) == 0 {
		return fmt.Errorf("parent has no organizations; enroll first")
	}
	orgID = orgs[0].Id

	flow := client.NewLoginFlow(plakman.Client())
	if err := flow.Login(api.LoginPasswordRequest{
		Account:  organization.IDPrefix(orgID) + "/admin@example.com",
		Password: "foobar",
	}); err != nil {
		return err
	}
	for !flow.State().IsFinal() {
		time.Sleep(10 * time.Millisecond)
	}
	if flow.State().Token == "" {
		return fmt.Errorf("login produced no token")
	}

	var badge api.TokenResponse
	if err := plakman.Client(client.WithToken(flow.State().Token)).
		Endpoint("/api/v1/auth/badge").
		Post(api.BadgeRequest{OrganizationId: orgID, Role: "owner"}, &badge); err != nil {
		return err
	}
	owner = plakman.Client(client.WithToken(badge.Token))
	return nil
}

func requireInstance(t *testing.T) {
	t.Helper()
	if !ready {
		t.Skip("no instance (make instance)")
	}
}

// call performs a request and returns the status, without the client's error
// wrapping getting in the way of asserting on it.
func call(t *testing.T, method, path string, body any) int {
	t.Helper()
	resp, err := owner.Endpoint(path).Do(method, body)
	require.NoError(t, err, "%s %s", method, path)
	defer resp.Body.Close() //nolint:errcheck
	return resp.StatusCode
}

// refused asserts that a request the server ought to reject is rejected with a
// client error. A 5xx here is the finding: the request was bad, the server knew
// it was bad, and it fell over instead of saying so.
func refused(t *testing.T, method, path string, body any) {
	t.Helper()
	code := call(t, method, path, body)
	require.Less(t, code, http.StatusInternalServerError,
		"%s %s answered %d; a refusal must be a 4xx, never a 5xx", method, path, code)
	require.GreaterOrEqual(t, code, http.StatusBadRequest,
		"%s %s answered %d; this request should have been refused", method, path, code)
}

// answers asserts an endpoint responds without falling over. Some of these have
// never been called by anything, so the bar is deliberately low: not a 5xx.
func answers(t *testing.T, method, path string) int {
	t.Helper()
	code := call(t, method, path, nil)
	require.Less(t, code, http.StatusInternalServerError,
		"%s %s answered %d", method, path, code)
	return code
}
