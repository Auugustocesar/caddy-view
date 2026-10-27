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
// order, with the reverse proxy upstreams of every route that serves the host merged without duplicates.
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
		upstreams := handlerUpstreams(route["handle"])
		for _, host := range routeHosts(route["match"]) {
			i, ok := byHost[host]
			if !ok {
				i = len(sites)
				byHost[host] = i
				sites = append(sites, Site{Server: server, Host: host, Upstreams: []string{}})
			}
			sites[i].Upstreams = appendUnique(sites[i].Upstreams, upstreams...)
		}
	}
	return sites
}

// routeHosts returns the hosts a route matches, or a single empty host (catch-all) when it names none.
func routeHosts(match any) []string {
	var hosts []string
	for _, m := range cfgpath.List(match) {
		for _, h := range cfgpath.List(cfgpath.Get(m, "host")) {
			if s, ok := h.(string); ok {
				hosts = append(hosts, s)
			}
		}
	}
	if len(hosts) == 0 {
		return []string{""}
	}
	return hosts
}

// handlerUpstreams collects the dial addresses of every reverse proxy in a handler chain, following subroutes.
func handlerUpstreams(handle any) []string {
	var out []string
	for _, h := range cfgpath.List(handle) {
		switch cfgpath.Get(h, "handler") {
		case "reverse_proxy":
			for _, u := range cfgpath.List(cfgpath.Get(h, "upstreams")) {
				if dial, ok := cfgpath.Get(u, "dial").(string); ok {
					out = appendUnique(out, dial)
				}
			}
		case "subroute":
			for _, r := range cfgpath.List(cfgpath.Get(h, "routes")) {
				out = appendUnique(out, handlerUpstreams(cfgpath.Get(r, "handle"))...)
			}
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
