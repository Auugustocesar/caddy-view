// Package cfgpath reads values out of decoded JSON (map[string]any and []any trees) without panicking on
// unexpected shapes. Caddy's configuration is large and loosely typed, so every accessor treats a wrong or
// missing shape as "nothing there".
package cfgpath

// Get walks nested JSON objects by key and returns the value found, or nil when any step is missing or is not an
// object. With no keys it returns v itself.
func Get(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// Map returns v as a JSON object, or nil if it is not one.
func Map(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// List returns v as a JSON array, or nil if it is not one.
func List(v any) []any {
	l, _ := v.([]any)
	return l
}
