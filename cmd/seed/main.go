// seed brings a freshly enrolled instance up to the state the e2e suite expects:
// the s3 integration installed, and a self-managed inventory to hang resources
// off. This is the credential-free half of `make seed` -- no gopass, no AWS, no
// Scaleway, no SFTP key.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	api "github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/client"
	"github.com/PlakarKorp/plakman/model/organization"
	"github.com/google/uuid"
)

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
	email := flag.String("email", "admin@example.com", "admin email")
	password := flag.String("password", "foobar", "admin password")
	invName := flag.String("inventory", "Self-managed inventory", "self-managed inventory name")
	flag.Parse()

	parent := client.NewServer(*parentURL)
	plakman := client.NewServer(*plakmanURL)

	// The root organization's id is the parent's: plakman mints its root under
	// the id the badge named, so the parent is the cheapest place to read it.
	var orgs []api.OrganizationResponse
	if err := parent.Client().Endpoint("/api/v1/account/organizations").Get(&orgs); err != nil {
		fail("read organization from parent", err)
	}
	if len(orgs) == 0 {
		fail("read organization from parent", errors.New("parent has no organizations; enroll first"))
	}
	orgID := orgs[0].Id
	ok("root organization", orgID)

	// A login names its organization, hence the <prefix>/<email> account form.
	account := organization.IDPrefix(orgID) + "/" + *email
	flow := client.NewLoginFlow(plakman.Client())
	if err := flow.Login(api.LoginPasswordRequest{Account: account, Password: *password}); err != nil {
		fail("login as "+account, err)
	}
	for !flow.State().IsFinal() {
		time.Sleep(10 * time.Millisecond)
	}
	if flow.State().Token == "" {
		fail("login as "+account, errors.New("login produced no token"))
	}
	ok("logged in", account)

	var badge api.TokenResponse
	if err := plakman.Client(client.WithToken(flow.State().Token)).
		Endpoint("/api/v1/auth/badge").
		Post(api.BadgeRequest{OrganizationId: orgID, Role: "owner"}, &badge); err != nil {
		fail("owner badge", err)
	}
	c := plakman.Client(client.WithToken(badge.Token))
	ok("owner badge")

	// The catalog is refreshed in the background after boot, so the integration
	// we want may simply not be listed yet.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if err := installIntegration(c, "s3"); err == nil {
			ok("s3 integration installed")
			break
		} else if time.Now().After(deadline) {
			fail("install s3 integration", err)
		}
		time.Sleep(2 * time.Second)
	}

	inventoryPath := "/api/v1/account/organizations/" + orgID.String() + "/inventories"
	var page api.PaginatedResponse[api.InventoryListResult]
	if err := c.EndpointWithLimitOffset(inventoryPath, 500, 0).Get(&page); err != nil {
		fail("list inventories", err)
	}
	for _, inv := range page.Items {
		if inv.Name == *invName {
			ok("self-managed inventory already present", inv.ID)
			fmt.Println("\nPASS  instance seeded (credential-free profile)")
			return
		}
	}

	var created api.InventoryCreateResponse
	if err := c.Endpoint(inventoryPath+"/self-managed").Post(api.InventorySelfManagedCreate{
		InventoryCreateRequest: api.InventoryCreateRequest{Name: *invName},
	}, &created); err != nil {
		fail("create self-managed inventory", err)
	}
	ok("self-managed inventory created", created.ID)

	fmt.Println("\nPASS  instance seeded (credential-free profile)")
}

var errNotInCatalog = errors.New("not in catalog")

func installIntegration(c *client.Client, name string) error {
	var catalog api.PaginatedResponse[availableIntegration]
	if err := c.Endpoint("/api/v1/integrations/catalog").Get(&catalog); err != nil {
		return err
	}
	for _, item := range catalog.Items {
		if item.Name != name {
			continue
		}
		var resp struct {
			ID uuid.UUID `json:"id"`
		}
		err := c.Endpoint("/api/v1/integrations/installed").
			Post(map[string]string{"name": name, "version": item.Version}, &resp)
		if err != nil {
			var httpErr *client.HttpError
			// Already installed is success for a seeder.
			if errors.As(err, &httpErr) && httpErr.Response.StatusCode == 409 {
				return nil
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %q", errNotInCatalog, name)
}

type availableIntegration struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
