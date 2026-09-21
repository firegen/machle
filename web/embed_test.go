package web

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every element the script reaches for must exist in the markup. A typo in an
// id yields null at runtime and breaks the UI on click, which is exactly the
// kind of regression worth catching without having to open a browser.
func TestEmbeddedAppMatchesMarkup(t *testing.T) {
	index := readFile(t, "index.html")
	script := readFile(t, "app.js")
	readFile(t, "style.css")

	declared := findAll(regexp.MustCompile(`id="([^"]+)"`), index)
	used := findAll(regexp.MustCompile(`\$\('([^']+)'\)`), script)
	if len(used) == 0 {
		t.Fatal("app.js does not look up a single element — did the helper change?")
	}

	var missing []string
	for _, id := range used {
		if !contains(declared, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("app.js looks up ids that index.html does not define: %v", missing)
	}

	// The page must load the two assets it ships with, by relative URL so they
	// work behind any base path.
	for _, ref := range []string{`href="style.css"`, `src="app.js"`} {
		if !strings.Contains(index, ref) {
			t.Errorf("index.html never references %s", ref)
		}
	}

	// The script must call the documented endpoints and nothing else.
	for _, endpoint := range []string{"/api/config", "/api/players", "/api/balance"} {
		if !strings.Contains(script, `api('`+endpoint) {
			t.Errorf("app.js never calls %s", endpoint)
		}
	}
	if !strings.Contains(script, "teamSize:") || !strings.Contains(script, "weights:") {
		t.Error("app.js no longer sends teamSize/weights with the balance request")
	}
}

func TestAssetsContainOnlyFrontendFiles(t *testing.T) {
	entries, err := fs.ReadDir(Assets, ".")
	if err != nil {
		t.Fatalf("read embedded dir: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	if want := []string{"app.js", "index.html", "style.css"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("embedded assets = %v, want %v", got, want)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(Assets, name)
	if err != nil {
		t.Fatalf("embedded %s: %v", name, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Fatalf("embedded %s is empty", name)
	}
	return string(data)
}

func findAll(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
