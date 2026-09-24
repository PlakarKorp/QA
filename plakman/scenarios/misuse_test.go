package scenarios

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// TestCloudInventoriesRefuseEmptyConfig posts each cloud inventory creator with
// nothing in it. None of these had ever been called, and none of them can be
// called for real here -- they want live cloud credentials -- but the refusal
// path is reachable without any, and it is the half that runs on a bad day.
func TestCloudInventoriesRefuseEmptyConfig(t *testing.T) {
	requireInstance(t)

	base := "/api/v1/account/organizations/" + orgID.String() + "/inventories/"
	for _, backend := range []string{"aws", "gcp", "k8s", "ovh", "scaleway", "vmware"} {
		t.Run(backend, func(t *testing.T) {
			refused(t, "POST", base+backend, map[string]any{})
		})
	}
}

// TestUnknownIdsAreRefused walks the endpoints that take an id and gives each a
// well-formed one that names nothing. The answer should be a 404, and must not
// be a 500: "I looked and it is not there" is a different thing from "I fell
// over looking".
func TestUnknownIdsAreRefused(t *testing.T) {
	requireInstance(t)

	missing := uuid.NewString()
	org := orgID.String()

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"test a connector that does not exist", "POST",
			"/api/v1/connectors/" + missing + "/test", nil},
		{"update a secret provider that does not exist", "PUT",
			"/api/v1/secret_providers/" + missing, map[string]any{"name": "nope"}},
		{"update an SLA template that does not exist", "POST",
			"/api/v1/account/organizations/" + org + "/slas/templates/" + missing, map[string]any{}},
		{"patch an SLA template that does not exist", "PATCH",
			"/api/v1/account/organizations/" + org + "/slas/templates/" + missing, map[string]any{}},
		{"run a scheduler task that does not exist", "POST",
			"/api/v1/scheduling/scheduler/tasks/" + missing, map[string]any{}},
		// Answers 200 today: RemovePackage has no not-found branch, so removing
		// something that was never there reports success. Idempotent DELETE is a
		// defensible choice, but every other id-taking route here 404s, so this
		// is either an inconsistency or a decision nobody has written down.
		{"delete a package that was never installed", "DELETE",
			"/api/v1/packages/not-an-integration/v0.0.0", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused(t, tc.method, tc.path, tc.body)
		})
	}
}

// TestMalformedBodiesAreRefused sends nonsense where a structured body is
// expected. Each of these is a route no client had exercised, so nothing had
// ever taken its error path.
func TestMalformedBodiesAreRefused(t *testing.T) {
	requireInstance(t)

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"a secret provider with no configuration", "POST",
			"/api/v1/account/organizations/" + orgID.String() + "/secret_providers", map[string]any{}},
		{"a package that is not a package", "POST",
			"/api/v1/packages", map[string]any{"name": "", "version": ""}},
		{"a license that is not a license", "PUT",
			"/api/v1/license", map[string]any{"license": "not-a-jwt"}},
		{"an offline enrollment with no license", "POST",
			"/api/v1/bootstrap/enroll/offline", map[string]any{}},
		{"a preview of the same", "POST",
			"/api/v1/bootstrap/enroll/offline/preview", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused(t, tc.method, tc.path, tc.body)
		})
	}
}

// TestConnectorTestOnRealConnector exercises the connector test endpoint the
// way it is meant to be used: against a connector that exists. Whether it
// reports reachable or not is the deployment's business; that it answers
// without falling over is ours.
func TestConnectorTestOnRealConnector(t *testing.T) {
	requireInstance(t)

	var page struct {
		Items []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"items"`
	}
	if err := owner.EndpointWithLimitOffset(
		"/api/v1/account/organizations/"+orgID.String()+"/connectors", 100, 0).Get(&page); err != nil {
		t.Skipf("cannot list connectors: %v", err)
	}
	if len(page.Items) == 0 {
		t.Skip("no connectors on this instance; the e2e scenarios clean up after themselves")
	}

	code := answers(t, "POST", fmt.Sprintf("/api/v1/connectors/%s/test", page.Items[0].ID))
	t.Logf("testing connector %q -> %d", page.Items[0].Name, code)
}
