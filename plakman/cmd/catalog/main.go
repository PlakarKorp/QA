// catalog prints the integration catalog as the instance sees it, which is the
// edition-filtered view -- not what the upstream index carries.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	api "github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/client"
	"github.com/PlakarKorp/plakman/model/organization"
)

type item struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Edition string `json:"edition"`
}

func main() {
	parentURL := flag.String("parent", "http://localhost:18081", "mock parent URL")
	plakmanURL := flag.String("plakman", "http://localhost:18080", "plakman URL")
	only := flag.String("name", "", "only this integration")
	printToken := flag.Bool("token", false, "print an owner badge and exit")
	flag.Parse()

	parent := client.NewServer(*parentURL)
	plakman := client.NewServer(*plakmanURL)

	var orgs []api.OrganizationResponse
	if err := parent.Client().Endpoint("/api/v1/account/organizations").Get(&orgs); err != nil || len(orgs) == 0 {
		fmt.Println("cannot read organization from parent:", err)
		os.Exit(1)
	}
	flow := client.NewLoginFlow(plakman.Client())
	if err := flow.Login(api.LoginPasswordRequest{
		Account:  organization.IDPrefix(orgs[0].Id) + "/admin@example.com",
		Password: "foobar",
	}); err != nil {
		fmt.Println("login:", err)
		os.Exit(1)
	}
	for !flow.State().IsFinal() {
		time.Sleep(10 * time.Millisecond)
	}
	var badge api.TokenResponse
	if err := plakman.Client(client.WithToken(flow.State().Token)).Endpoint("/api/v1/auth/badge").
		Post(api.BadgeRequest{OrganizationId: orgs[0].Id, Role: "owner"}, &badge); err != nil {
		fmt.Println("badge:", err)
		os.Exit(1)
	}

	if *printToken {
		fmt.Println(badge.Token)
		return
	}

	var page api.PaginatedResponse[item]
	if err := plakman.Client(client.WithToken(badge.Token)).
		Endpoint("/api/v1/integrations/catalog").Get(&page); err != nil {
		fmt.Println("catalog:", err)
		os.Exit(1)
	}
	fmt.Printf("catalog: %d entries\n", len(page.Items))
	for _, it := range page.Items {
		if *only != "" && it.Name != *only {
			continue
		}
		fmt.Printf("  %-22s %-14s %s\n", it.Name, it.Version, it.Edition)
	}
}
