package cfgpath

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGet(t *testing.T) {
	doc := decode(t, `{"a":{"b":{"c":42,"d":null,"list":[1,2]}},"s":"text"}`)
	tests := []struct {
		name string
		root any
		keys []string
		want any
	}{
		{"nested value", doc, []string{"a", "b", "c"}, float64(42)},
		{"nested object", doc, []string{"a", "b", "list"}, []any{float64(1), float64(2)}},
		{"explicit null", doc, []string{"a", "b", "d"}, nil},
		{"missing key", doc, []string{"a", "x", "c"}, nil},
		{"missing at the end", doc, []string{"a", "b", "zzz"}, nil},
		{"walks through a non-object", doc, []string{"s", "x"}, nil},
		{"walks through an array", doc, []string{"a", "b", "list", "0"}, nil},
		{"no keys returns the value itself", "plain", nil, "plain"},
		{"nil root", nil, []string{"a"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Get(tt.root, tt.keys...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Get(%v) = %#v, want %#v", tt.keys, got, tt.want)
			}
		})
	}
}

func TestMap(t *testing.T) {
	obj := map[string]any{"k": "v"}
	if got := Map(obj); !reflect.DeepEqual(got, obj) {
		t.Errorf("Map(object) = %v, want %v", got, obj)
	}
	for name, v := range map[string]any{"nil": nil, "string": "x", "number": 1.5, "array": []any{}} {
		if got := Map(v); got != nil {
			t.Errorf("Map(%s) = %v, want nil", name, got)
		}
	}
}

func TestList(t *testing.T) {
	list := []any{"a", float64(2)}
	if got := List(list); !reflect.DeepEqual(got, list) {
		t.Errorf("List(array) = %v, want %v", got, list)
	}
	for name, v := range map[string]any{"nil": nil, "string": "x", "number": 1.5, "object": map[string]any{}} {
		if got := List(v); got != nil {
			t.Errorf("List(%s) = %v, want nil", name, got)
		}
	}
}
