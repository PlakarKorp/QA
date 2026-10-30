// ledger says what a QA run actually touched.
//
// It reads plakman's own log rather than instrumenting anything. The daemon
// prints every route it mounts at startup ("Installing  GET /connectors/{id}")
// and every request it answers ("outgoing response ... method=GET path=..."),
// so the universe and the coverage are both already there, for any client --
// the Go suite, the UI, curl, a customer.
//
//	ledger /tmp/plakman-qa/logs/plakman.*.log
//
// A route with no request against it is the point of the report: a green run
// says what we tested, and only this list says what we are still guessing at.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type route struct {
	method string
	tmpl   string
}

var (
	reInstall = regexp.MustCompile(`^INFO: Installing\s+([A-Z]+)\s+(\S+)`)
	reRequest = regexp.MustCompile(`outgoing response .*?status_code=(\d+) method=([A-Z]+) path=(\S+)`)
)

// apiPrefix is where the versioned API is mounted. Routes are logged without
// it at startup and with it on every request, so one side has to be normalised.
const apiPrefix = "/api/v1"

// specificity scores how literal a template is: a path can match several, and
// the server routes it to the most specific, so the ledger has to as well.
// /inventories/coverage matches both itself and /inventories/{inventory_id},
// and crediting the wildcard leaves the literal route looking untouched.
func specificity(tmpl string) int {
	n := 0
	for _, seg := range strings.Split(strings.Trim(tmpl, "/"), "/") {
		if !strings.HasPrefix(seg, "{") {
			n += 2 // a literal segment beats a wildcard
		} else if !strings.HasSuffix(seg, "...}") {
			n++ // a single-segment wildcard beats a trailing one
		}
	}
	return n
}

// matches reports whether a concrete path is an instance of a route template.
// Templates use {name} for one segment and {name...} for the rest, which is
// net/http's own pattern syntax.
func matches(tmpl, path string) bool {
	t := strings.Split(strings.Trim(tmpl, "/"), "/")
	p := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range t {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}") {
			return len(p) >= i // trailing wildcard swallows the rest
		}
		if i >= len(p) {
			return false
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg != p[i] {
			return false
		}
	}
	return len(p) == len(t)
}

func main() {
	showAll := flag.Bool("all", false, "list exercised routes too, not just untouched ones")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: ledger [-all] <plakman log>...")
		os.Exit(2)
	}

	universe := map[route]bool{}
	hits := map[route]int{}
	type req struct {
		method, path string
		status       string
	}
	var requests []req

	for _, name := range flag.Args() {
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
			os.Exit(1)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if m := reInstall.FindStringSubmatch(line); m != nil {
				universe[route{m[1], m[2]}] = true
				continue
			}
			if m := reRequest.FindStringSubmatch(line); m != nil {
				requests = append(requests, req{m[2], m[3], m[1]})
			}
		}
		_ = f.Close()
	}

	// Attribute each request to the route it was served by. A request whose
	// path matches nothing is worth seeing: it is either a route the daemon
	// never announced (the kloset group mounts without logging) or a path that
	// fell through to the UI, which is what a removed API route looks like.
	// The v2 surfaces are mounted without announcing their routes, so the
	// ledger cannot credit them; they are counted apart rather than listed as
	// strays, one line per surface.
	unmatched := map[string]int{}
	other := map[string]int{}
	for _, r := range requests {
		if surface := otherSurface(r.path); surface != "" {
			other[surface]++
			continue
		}
		trimmed := strings.TrimPrefix(r.path, apiPrefix)
		best, bestScore, found := route{}, -1, false
		for rt := range universe {
			if rt.method != r.method {
				continue
			}
			if !matches(rt.tmpl, trimmed) && !matches(rt.tmpl, r.path) {
				continue
			}
			// Ties are broken on the template string so a run is reproducible:
			// ranging a map is not ordered.
			if sc := specificity(rt.tmpl); sc > bestScore ||
				(sc == bestScore && rt.tmpl < best.tmpl) {
				best, bestScore, found = rt, sc, true
			}
		}
		if found {
			hits[best]++
		} else {
			unmatched[r.method+" "+r.path]++
		}
	}

	var touched, untouched []route
	for rt := range universe {
		if hits[rt] > 0 {
			touched = append(touched, rt)
		} else {
			untouched = append(untouched, rt)
		}
	}
	byRoute := func(s []route) func(i, j int) bool {
		return func(i, j int) bool {
			if s[i].tmpl != s[j].tmpl {
				return s[i].tmpl < s[j].tmpl
			}
			return s[i].method < s[j].method
		}
	}
	sort.Slice(touched, byRoute(touched))
	sort.Slice(untouched, byRoute(untouched))

	total := len(universe)
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(len(touched)) / float64(total)
	}
	fmt.Printf("routes exercised   %d/%d  (%.0f%%)\n", len(touched), total, pct)
	fmt.Printf("requests seen      %d\n", len(requests))

	if *showAll && len(touched) > 0 {
		fmt.Printf("\nexercised:\n")
		for _, rt := range touched {
			fmt.Printf("  %-7s %-58s %d\n", rt.method, rt.tmpl, hits[rt])
		}
	}

	if len(untouched) > 0 {
		fmt.Printf("\nnever touched (%d):\n", len(untouched))
		for _, rt := range untouched {
			fmt.Printf("  %-7s %s\n", rt.method, rt.tmpl)
		}
	}

	if len(other) > 0 {
		fmt.Printf("\nrequests to surfaces the daemon does not announce:\n")
		keys := make([]string, 0, len(other))
		for k := range other {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-70s %d\n", k, other[k])
		}
	}

	if len(unmatched) > 0 {
		fmt.Printf("\nrequests matching no announced route (%d distinct):\n", len(unmatched))
		keys := make([]string, 0, len(unmatched))
		for k := range unmatched {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-70s %d\n", k, unmatched[k])
		}
	}
}

// otherSurface names the surface a path belongs to when it is one the daemon
// mounts without logging its routes: /api/v2, the internal machine protocol
// and the v2 UI contract. Empty for the v1 API and everything else.
func otherSurface(path string) string {
	for _, prefix := range []string{"/api/v2/", "/api/internal/", "/ui/v2/"} {
		if strings.HasPrefix(path, prefix) {
			return prefix + "..."
		}
	}
	return ""
}
