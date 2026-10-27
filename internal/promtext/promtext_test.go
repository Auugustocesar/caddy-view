package promtext

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Sample
	}{
		{"a sample without labels", "up 1\n", []Sample{{Name: "up", Labels: map[string]string{}, Value: 1}}},
		{"labels", `caddy_http_requests_total{handler="subroute",server="srv0"} 27` + "\n",
			[]Sample{{Name: "caddy_http_requests_total", Labels: map[string]string{"handler": "subroute", "server": "srv0"}, Value: 27}}},
		{"empty braces", "up{} 1\n", []Sample{{Name: "up", Labels: map[string]string{}, Value: 1}}},
		{"a trailing comma in the labels", `m{a="b",} 2` + "\n", []Sample{{Name: "m", Labels: map[string]string{"a": "b"}, Value: 2}}},
		{"spaces around the parts", "  m { a = \"b\" ,  c=\"d\" }   3.5  \n",
			[]Sample{{Name: "m", Labels: map[string]string{"a": "b", "c": "d"}, Value: 3.5}}},
		{"escapes in a label value", `m{path="a\\b\"c\nd"} 1` + "\n", []Sample{{Name: "m", Labels: map[string]string{"path": "a\\b\"c\nd"}, Value: 1}}},
		{"a comma and a brace inside a value", `m{a="x,y}z"} 1` + "\n", []Sample{{Name: "m", Labels: map[string]string{"a": "x,y}z"}, Value: 1}}},
		{"a timestamp is ignored", `m{a="b"} 4 1761566400000` + "\n", []Sample{{Name: "m", Labels: map[string]string{"a": "b"}, Value: 4}}},
		{"exponent and negative numbers", "m 1.5e3\nn -2\n", []Sample{
			{Name: "m", Labels: map[string]string{}, Value: 1500}, {Name: "n", Labels: map[string]string{}, Value: -2}}},
		{"colons in the name", "job:rate:5m 1\n", []Sample{{Name: "job:rate:5m", Labels: map[string]string{}, Value: 1}}},
		{"comments and blank lines are skipped", "# HELP up Is it up.\n# TYPE up gauge\n\n  \nup 1\n# trailing comment\n",
			[]Sample{{Name: "up", Labels: map[string]string{}, Value: 1}}},
		{"CRLF line ends", "a 1\r\nb 2\r\n", []Sample{{Name: "a", Labels: map[string]string{}, Value: 1}, {Name: "b", Labels: map[string]string{}, Value: 2}}},
		{"no newline at the end", "a 1", []Sample{{Name: "a", Labels: map[string]string{}, Value: 1}}},
		{"nothing at all", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("samples = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseSpecialValues(t *testing.T) {
	got, err := Parse([]byte("a +Inf\nb -Inf\nc NaN\nd Inf\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || !math.IsInf(got[0].Value, 1) || !math.IsInf(got[1].Value, -1) || !math.IsNaN(got[2].Value) || !math.IsInf(got[3].Value, 1) {
		t.Errorf("values = %v, want +Inf, -Inf, NaN, +Inf", got)
	}
}

func TestParseRealCaddyHistogramLine(t *testing.T) {
	line := `caddy_http_request_duration_seconds_bucket{code="101",handler="subroute",method="GET",server="srv1",le="+Inf"} 6` + "\n"
	got, err := Parse([]byte(line))
	if err != nil || len(got) != 1 || got[0].Labels["le"] != "+Inf" || got[0].Value != 6 {
		t.Errorf("Parse = %+v, %v, want one bucket sample with le=+Inf and value 6", got, err)
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	tests := map[string]string{
		"no value":                     "up\n",
		"a value that is text":         "up fast\n",
		"a name starting with a digit": "1up 1\n",
		"an unterminated label set":    `m{a="b" 1` + "\n",
		"an unterminated value":        `m{a="b} 1` + "\n",
		"a label without a value":      `m{a} 1` + "\n",
		"an unquoted label value":      `m{a=b} 1` + "\n",
		"a bad escape":                 `m{a="\q"} 1` + "\n",
		"a bad label name":             `m{1a="b"} 1` + "\n",
		"junk after the value":         "up 1 2 3\n",
		"a timestamp that is text":     "up 1 yesterday\n",
		"text after the labels":        `m{a="b"}x 1` + "\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(input))
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("error = %v, want ErrSyntax", err)
			}
		})
	}
}

func TestParseErrorsNameTheLine(t *testing.T) {
	_, err := Parse([]byte("a 1\nb 2\nbroken line here\nc 3\n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error = %v, want it to name line 3", err)
	}
}

func TestParseKeepsNothingFromAFailedInput(t *testing.T) {
	got, err := Parse([]byte("a 1\nbroken\n"))
	if err == nil || got != nil {
		t.Errorf("Parse = %v, %v, want no samples and an error", got, err)
	}
}

func TestFormatLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"none", nil, ""},
		{"empty", map[string]string{}, ""},
		{"one", map[string]string{"server": "srv0"}, `{server="srv0"}`},
		{"sorted by name", map[string]string{"server": "srv0", "code": "2xx"}, `{code="2xx",server="srv0"}`},
		{"escaped values", map[string]string{"a": "x\"y\\z\nw"}, `{a="x\"y\\z\nw"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatLabels(tt.labels); got != tt.want {
				t.Errorf("FormatLabels = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatLabelsIsStableAcrossMapOrder(t *testing.T) {
	labels := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	first := FormatLabels(labels)
	for range 20 {
		if got := FormatLabels(labels); got != first {
			t.Fatalf("FormatLabels changed between calls: %q then %q", first, got)
		}
	}
}
