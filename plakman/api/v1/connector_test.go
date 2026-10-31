// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv1

import (
	"testing"

	"github.com/PlakarKorp/QA/plakman/api/harness"
	"github.com/stretchr/testify/require"
)

type connector struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Protocol    string `json:"protocol"`
	Root        string `json:"root"`
	Revision    int    `json:"revision"`
	Integration struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"integration"`
}

// storeFixture is an inventory holding one object-storage resource, which is
// what a store connector serves. Its endpoint is the loopback, where nothing
// listens on the port the connectors name, so a connector on it can be
// created, read and tested, and the test always fails fast.
func storeFixture(t *testing.T) (inventory, resource) {
	t.Helper()
	inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
	r := addResource(t, pcpctl, inv.ID, "bucket", "--class", "object-storage", "--subclass", "s3", "--service", "s3",
		"--entry", `{"endpoints":[{"endpoint":"127.0.0.1"}]}`)
	return inv, r
}

// createStore creates an s3 store connector on the resource and removes it
// when the test ends.
func createStore(t *testing.T, name string, r resource, extra ...string) connector {
	t.Helper()
	var c connector
	pcpctl.JSON(t, &c, append([]string{"connector", "create", name, "--type", "store", "--protocol", "s3",
		"--integration", "s3", "--resource", r.URNID,
		"--field", "access_key=qa", "--field", "secret_access_key=qa", "--field", "root=/bucket"}, extra...)...)
	t.Cleanup(func() { pcpctl.Run(t, "connector", "delete", c.ID, "--yes") })
	return c
}

func connectorIDs(t *testing.T, args ...string) []string {
	t.Helper()
	var cs page[connector]
	pcpctl.JSON(t, &cs, append([]string{"connector", "list"}, args...)...)
	ids := make([]string, len(cs.Items))
	for i, c := range cs.Items {
		ids[i] = c.ID
	}
	return ids
}

// TestConnectorLifecycle creates a store connector on a resource, reads it
// back by id and by name, finds it in the listing of its type and not in the
// others, deletes it, and sees it gone.
func TestConnectorLifecycle(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	name := harness.Unique("qa-store")

	c := createStore(t, name, r)
	require.NotEmpty(t, c.ID)
	require.Equal(t, name, c.Name)
	require.Equal(t, "store", c.Type)
	require.Equal(t, "s3", c.Protocol)
	require.Equal(t, "s3", c.Integration.Name, "a name picks the installed integration")
	require.NotEmpty(t, c.Integration.Version)

	var byID connector
	pcpctl.JSON(t, &byID, "connector", "get", c.ID)
	require.Equal(t, c, byID)
	var byName connector
	pcpctl.JSON(t, &byName, "connector", "get", name)
	require.Equal(t, c.ID, byName.ID)

	require.Contains(t, connectorIDs(t, "--type", "store"), c.ID)
	require.Contains(t, connectorIDs(t), c.ID, "store is the default listing")
	require.NotContains(t, connectorIDs(t, "--type", "source"), c.ID)
	require.NotContains(t, connectorIDs(t, "--type", "destination"), c.ID)

	out := pcpctl.OK(t, "connector", "delete", c.ID, "--yes")
	require.Contains(t, out, c.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "connector", "get", c.ID)
	pcpctl.Fails(t, harness.ExitNotFound, "connector", "get", name)
	require.NotContains(t, connectorIDs(t, "--type", "store"), c.ID)
}

// TestConnectorCreateMisuse: the creations pcpctl or the API refuse, each
// with its exit code, and nothing created by any of them.
func TestConnectorCreateMisuse(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	name := harness.Unique("qa-bad")

	t.Run("no type", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "connector", "create", name, "--protocol", "s3", "--integration", "s3", "--resource", r.URNID)
	})
	t.Run("no protocol", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitUsage, "connector", "create", name, "--type", "store", "--integration", "s3", "--resource", r.URNID)
	})
	t.Run("unknown type", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "connector", "create", name, "--type", "disk", "--protocol", "s3", "--integration", "s3", "--resource", r.URNID)
	})
	t.Run("no resource", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "connector", "create", name, "--type", "store", "--protocol", "s3", "--integration", "s3")
	})
	t.Run("no integration", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "connector", "create", name, "--type", "store", "--protocol", "s3", "--resource", r.URNID)
	})
	t.Run("integration not installed", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "create", name, "--type", "store", "--protocol", "s3", "--integration", "nope", "--resource", r.URNID)
	})
	t.Run("absent resource", func(t *testing.T) {
		// The server reports a urn that names nothing as a bad parameter,
		// not as a not-found, so pcpctl exits 1 rather than 3.
		res := pcpctl.Fails(t, harness.ExitError, "connector", "create", name, "--type", "store", "--protocol", "s3", "--integration", "s3", "--resource", missing)
		if !harness.OnV2() {
			// v2 answers 500 for a urn that names nothing: api/README.md.
			require.Contains(t, res.Stderr, "Bad parameter")
		}
	})
	t.Run("secret in an absent provider", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "create", name, "--type", "store", "--protocol", "s3", "--integration", "s3", "--resource", r.URNID,
			"--secret-field", "secret_access_key=novault")
	})
	t.Run("a field that is not name=value", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "connector", "create", name, "--type", "store", "--protocol", "s3", "--integration", "s3", "--resource", r.URNID,
			"--field", "root")
	})
	pcpctl.Fails(t, harness.ExitNotFound, "connector", "get", name)
}

// TestConnectorDeleteMisuse: deleting nothing, twice, and without --yes.
func TestConnectorDeleteMisuse(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)

	t.Run("absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "delete", missing, "--yes")
	})
	t.Run("twice", func(t *testing.T) {
		c := createStore(t, harness.Unique("qa-store"), r)
		pcpctl.OK(t, "connector", "delete", c.ID, "--yes")
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "delete", c.ID, "--yes")
	})
	t.Run("without --yes", func(t *testing.T) {
		c := createStore(t, harness.Unique("qa-store"), r)
		res := pcpctl.Fails(t, harness.ExitError, "connector", "delete", c.ID)
		require.Contains(t, res.Stderr, c.Name)
		pcpctl.OK(t, "connector", "get", c.ID)
	})
}

// TestConnectorDuplicateName: connectors have no uniqueness check, so two may
// share a name; get by that name is then ambiguous until one goes.
func TestConnectorDuplicateName(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	name := harness.Unique("qa-twin-store")

	first := createStore(t, name, r)
	second := createStore(t, name, r)
	require.NotEqual(t, first.ID, second.ID)
	pcpctl.Fails(t, harness.ExitError, "connector", "get", name)

	pcpctl.OK(t, "connector", "delete", first.ID, "--yes")
	var left connector
	pcpctl.JSON(t, &left, "connector", "get", name)
	require.Equal(t, second.ID, left.ID)
}

// TestConnectorUpdate renames a connector. An update replaces the connector,
// so it restates the resource; one that does not is refused and changes
// nothing.
func TestConnectorUpdate(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)

	var updated connector
	pcpctl.JSON(t, &updated, "connector", "update", c.ID, "--name", c.Name+"-renamed", "--resource", r.URNID)
	require.Equal(t, c.ID, updated.ID)
	require.Equal(t, c.Name+"-renamed", updated.Name)

	var got connector
	pcpctl.JSON(t, &got, "connector", "get", c.ID)
	require.Equal(t, c.Name+"-renamed", got.Name)
	require.Greater(t, got.Revision, c.Revision, "an update is a new revision")

	t.Run("without the resource", func(t *testing.T) {
		if harness.OnV2() {
			// v2 patches: what an update does not name is kept, the
			// resource included, so the rename goes through.
			pcpctl.OK(t, "connector", "update", c.ID, "--name", "orphan")
			pcpctl.JSON(t, &got, "connector", "get", c.ID)
			require.Equal(t, "orphan", got.Name)
			return
		}
		pcpctl.Fails(t, harness.ExitError, "connector", "update", c.ID, "--name", "orphan")
		pcpctl.JSON(t, &got, "connector", "get", c.ID)
		require.Equal(t, c.Name+"-renamed", got.Name)
	})
	t.Run("absent", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "connector", "update", missing, "--name", "ghost", "--resource", r.URNID)
	})
}

// TestConnectorTest: a connector whose endpoint answers nothing fails its
// test, and says so with a non-zero exit in every format; testing nothing is a
// not-found.
func TestConnectorTest(t *testing.T) {
	requireInstance(t)
	pcpctl.Fails(t, harness.ExitNotFound, "connector", "test", missing)

	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r, "--field", "port=1", "--field", "use_tls=false")
	res := pcpctl.Fails(t, harness.ExitError, "connector", "test", c.ID, "-o", "json")
	require.Contains(t, res.Stdout, `"ok": false`)
	require.Contains(t, res.Stdout, "connection refused", "the verdict carries the backend's own error")

	t.Run("resource without an endpoint", func(t *testing.T) {
		inv := createInventory(t, pcpctl, harness.Unique("qa-inv"))
		bare := addResource(t, pcpctl, inv.ID, "bare", "--class", "object-storage", "--subclass", "s3")
		c := createStore(t, harness.Unique("qa-store"), bare)
		res := pcpctl.Fails(t, harness.ExitError, "connector", "test", c.ID)
		if !harness.OnV2() {
			// v2 runs the test anyway and reports the failure to connect.
			require.Contains(t, res.Stderr, "no endpoints", "there is nowhere to test, and the refusal says so")
		}
	})
}

// TestConnectorIsolation: a connector is the organization's own. A
// sub-organization neither lists nor reads it, and cannot delete it.
func TestConnectorIsolation(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)
	child := createOrg(t, pcpctl, harness.Unique("qa-org"))
	in := pcpctl.In(child.ID)

	var cs page[connector]
	in.JSON(t, &cs, "connector", "list", "--type", "store")
	for _, x := range cs.Items {
		require.NotEqual(t, c.ID, x.ID)
	}
	in.Fails(t, harness.ExitNotFound, "connector", "get", c.ID)
	in.Fails(t, harness.ExitNotFound, "connector", "delete", c.ID, "--yes")
	pcpctl.OK(t, "connector", "get", c.ID)
}

// TestSnapshotsOfStore: a fresh store holds no snapshots, and the listing
// needs exactly one of --store and --source.
func TestSnapshotsOfStore(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)

	var snaps page[map[string]any]
	pcpctl.JSON(t, &snaps, "snapshot", "list", "--store", c.ID)
	require.Equal(t, 0, snaps.Total)
	require.Empty(t, snaps.Items)
	pcpctl.JSON(t, &snaps, "snapshot", "list", "--store", c.Name)
	require.Empty(t, snaps.Items, "--store takes a name")

	t.Run("neither", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "snapshot", "list")
	})
	t.Run("both", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitError, "snapshot", "list", "--store", c.ID, "--source", c.ID)
	})
	t.Run("absent store", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "list", "--store", missing)
		pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "list", "--store", harness.Unique("no-such-store"))
	})
	t.Run("absent snapshot", func(t *testing.T) {
		pcpctl.Fails(t, harness.ExitNotFound, "snapshot", "get", missing, "--store", c.ID)
	})
}

// TestJobsOfConnector: a fresh connector has no jobs, by connector and by
// status; the job reads of something absent are not-founds.
func TestJobsOfConnector(t *testing.T) {
	requireInstance(t)
	_, r := storeFixture(t)
	c := createStore(t, harness.Unique("qa-store"), r)

	var jobs page[map[string]any]
	pcpctl.JSON(t, &jobs, "job", "list", "--connector", c.ID)
	require.Empty(t, jobs.Items)
	pcpctl.JSON(t, &jobs, "job", "list", "--status", "failed")
	for _, j := range jobs.Items {
		require.Equal(t, "failed", j["status"])
	}

	// A filter on a connector that does not exist is an empty page, not a
	// refusal: the filter is a value, and no job carries it.
	pcpctl.JSON(t, &jobs, "job", "list", "--connector", missing)
	require.Empty(t, jobs.Items)
	pcpctl.Fails(t, harness.ExitNotFound, "job", "log", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "job", "state", missing)
	pcpctl.Fails(t, harness.ExitNotFound, "job", "cancel", missing)

	t.Run("unknown status", func(t *testing.T) {
		finding(t, "GET /scheduling/jobs?status=bogus answers 500; a bad filter should be a 400")
		pcpctl.Fails(t, harness.ExitError, "job", "list", "--status", "bogus")
	})
}
