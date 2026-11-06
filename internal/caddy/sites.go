package caddy

import (
	"slices"
	"sort"

	"github.com/Auugustocesar/caddy-view/internal/cfgpath"
)

// Site is one (server, host) pair found in the running configuration.
// A route without a host matcher is a catch-all and has an empty Host.
type Site struct {
	Server    string
	Host      string
	Upstreams []string
}

// ExtractSites lists the sites of a running configuration: per HTTP server in name order, per host in route
// order, with the reverse proxy upstreams of every route that serves the host merged without duplicates. Routes inside
// a subroute are followed, including the ones that name hosts of their own (see routeSites).
// Parts of the configuration that do not have the expected shape are skipped.
func ExtractSites(cfg map[string]any) []Site {
	sites := []Site{}
	servers := cfgpath.Map(cfgpath.Get(cfg, "apps", "http", "servers"))

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sites = append(sites, serverSites(name, cfgpath.Map(servers[name]))...)
	}
	return sites
}

func serverSites(server string, srv map[string]any) []Site {
	var sites []Site
	byHost := map[string]int{}
	for _, r := range cfgpath.List(srv["routes"]) {
		route := cfgpath.Map(r)
		if route == nil {
			continue
		}
		for _, s := range routeSites(route, nil, true) {
			i, ok := byHost[s.host]
			if !ok {
				i = len(sites)
				byHost[s.host] = i
				sites = append(sites, Site{Server: server, Host: s.host, Upstreams: []string{}})
			}
			sites[i].Upstreams = appendUnique(sites[i].Upstreams, s.upstreams...)
		}
	}
	return sites
}

// served is what one host gets from a route: the upstreams it proxies to, possibly none.
type served struct {
	host      string
	upstreams []string
}

// routeSites lists the hosts a route serves, with their upstreams. inherited is the hosts of the route that holds this
// one in a subroute, which a route without a host matcher of its own serves, and top says it is a route of the server
// itself rather than one inside a subroute.
//
// A route can fan out by host: the Caddyfile adapter turns `*.example.com { @app host app.example.com ... }` into a
// route for the wildcard whose subroute holds one route per subdomain. Those subdomains are the sites, and the wildcard
// is only what holds them, so it is not listed on top of them. It is listed when it proxies on its own, and a top-level
// host is always listed so that a site that proxies nothing (a redirect, a file server) is not lost.
func routeSites(route map[string]any, inherited []string, top bool) []served {
	explicit := matchedHosts(route["match"])
	hosts := explicit
	if len(hosts) == 0 {
		hosts = inherited
	}
	if len(hosts) == 0 {
		hosts = []string{""} // a catch-all
	}

	var own []string
	var inner []served
	fanned := false
	for _, h := range cfgpath.List(route["handle"]) {
		switch cfgpath.Get(h, "handler") {
		case "reverse_proxy":
			own = appendUnique(own, dials(h)...)
		case "subroute":
			for _, r := range cfgpath.List(cfgpath.Get(h, "routes")) {
				child := cfgpath.Map(r)
				if child == nil {
					continue
				}
				found := routeSites(child, hosts, false)
				if len(matchedHosts(child["match"])) > 0 && len(found) > 0 {
					fanned = true
				}
				inner = append(inner, found...)
			}
		}
	}

	var out []served
	switch {
	case len(own) > 0:
		for _, host := range hosts {
			out = append(out, served{host: host, upstreams: own})
		}
	case !fanned && (top || len(explicit) > 0):
		for _, host := range hosts {
			out = append(out, served{host: host})
		}
	}
	return append(out, inner...)
}

// matchedHosts returns the hosts named by the host matchers of a route, or nil when it names none.
func matchedHosts(match any) []string {
	var hosts []string
	for _, m := range cfgpath.List(match) {
		for _, h := range cfgpath.List(cfgpath.Get(m, "host")) {
			if s, ok := h.(string); ok {
				hosts = append(hosts, s)
			}
		}
	}
	return hosts
}

// dials returns the dial addresses of the upstreams of a reverse proxy handler.
func dials(handler any) []string {
	var out []string
	for _, u := range cfgpath.List(cfgpath.Get(handler, "upstreams")) {
		if dial, ok := cfgpath.Get(u, "dial").(string); ok {
			out = appendUnique(out, dial)
		}
	}
	return out
}

func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		if !slices.Contains(list, item) {
			list = append(list, item)
		}
	}
	return list
}
