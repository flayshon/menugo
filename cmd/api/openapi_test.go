package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestOpenAPIDocumentsEveryRoute keeps docs/openapi.yaml in step with
// routes.go: every route must be documented, and every documented operation
// must exist. CORS preflight (OPTIONS) routes aren't API operations and are
// left out.
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	routesSrc, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var routes []string
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ([^"]+)"`).FindAllStringSubmatch(string(routesSrc), -1) {
		if m[1] != "OPTIONS" {
			routes = append(routes, m[1]+" "+m[2])
		}
	}
	if len(routes) < 50 {
		t.Fatalf("found only %d routes in routes.go; has its format changed?", len(routes))
	}

	// Paths are indented two spaces under "paths:", operations four.
	pathRX := regexp.MustCompile(`^  (/\S+):\s*$`)
	methodRX := regexp.MustCompile(`^    (get|put|post|patch|delete):\s*$`)

	var documented []string
	inPaths, path := false, ""
	for line := range strings.Lines(string(spec)) {
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#"):
			inPaths = false // the next top-level key
		case !inPaths:
		case pathRX.MatchString(line):
			path = pathRX.FindStringSubmatch(line)[1]
		case methodRX.MatchString(line):
			method := strings.ToUpper(methodRX.FindStringSubmatch(line)[1])
			documented = append(documented, method+" "+path)
		}
	}

	for _, r := range routes {
		if !slices.Contains(documented, r) {
			t.Errorf("route %q is not documented in docs/openapi.yaml", r)
		}
	}
	for _, d := range documented {
		if !slices.Contains(routes, d) {
			t.Errorf("docs/openapi.yaml documents %q, which isn't a route", d)
		}
	}
}
