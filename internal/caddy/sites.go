package caddy

import "sort"

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
	servers := asMap(dig(cfg, "apps", "http", "servers"))

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sites = append(sites, serverSites(name, asMap(servers[name]))...)
	}
	return sites
}

func serverSites(server string, srv map[string]any) []Site {
	var sites []Site
	byHost := map[string]int{}
	for _, r := range asList(srv["routes"]) {
		route := asMap(r)
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
	for _, m := range asList(match) {
		for _, h := range asList(asMap(m)["host"]) {
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
	for _, h := range asList(handle) {
		handler := asMap(h)
		switch handler["handler"] {
		case "reverse_proxy":
			for _, u := range asList(handler["upstreams"]) {
				if dial, ok := asMap(u)["dial"].(string); ok {
					out = appendUnique(out, dial)
				}
			}
		case "subroute":
			for _, r := range asList(handler["routes"]) {
				out = appendUnique(out, handlerUpstreams(asMap(r)["handle"])...)
			}
		}
	}
	return out
}

func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		found := false
		for _, existing := range list {
			if existing == item {
				found = true
				break
			}
		}
		if !found {
			list = append(list, item)
		}
	}
	return list
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// dig walks nested JSON objects and returns nil when any step is missing.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}
