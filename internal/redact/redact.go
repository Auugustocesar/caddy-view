// Package redact hides secrets in a Caddy configuration before it is shown to anyone.
package redact

import "strings"

// Placeholder replaces the value of every sensitive key.
const Placeholder = "[redacted]"

// exactNames are sensitive on their own. Keys are compared lowercase with hyphens turned into underscores, so
// HTTP header names such as "X-Api-Key" are covered too.
var exactNames = map[string]bool{
	"key":                 true,
	"token":               true,
	"secret":              true,
	"password":            true,
	"passwd":              true,
	"authorization":       true,
	"proxy_authorization": true,
	"cookie":              true,
	"set_cookie":          true,
}

// suffixes mark names such as api_token, client_secret, db_password or signing_key.
var suffixes = []string{"_key", "_token", "_secret", "_password", "_passwd"}

// notSecret lists names that match a suffix but hold public material.
var notSecret = map[string]bool{"public_key": true}

// Config returns a deep copy of cfg in which the values of sensitive keys (passwords, tokens, secrets, private
// keys, credentials in headers) are replaced by Placeholder. Placeholders such as {env.TOKEN} or {file./path}
// are references, not secrets, and are kept. cfg is never modified.
func Config(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}
	return redactMap(cfg)
}

func redactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if isSensitive(k) {
			out[k] = hide(v)
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return redactMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = redactValue(item)
		}
		return out
	default:
		return v
	}
}

// hide returns what to show instead of the value of a sensitive key. Empty values and null have nothing to
// hide, and placeholders are references to a secret, not the secret.
func hide(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" || isPlaceholder(t) {
			return t
		}
	}
	return Placeholder
}

func isPlaceholder(s string) bool {
	return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && !strings.ContainsAny(s, " \t\r\n")
}

func isSensitive(key string) bool {
	name := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	if notSecret[name] {
		return false
	}
	if exactNames[name] {
		return true
	}
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}
