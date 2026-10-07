package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// A provider's Save carries what its editor's Names & levels changed
// (modelPrefs), and makes it with the rest (ARNO on Discord: each level
// ticked was applied on its own, before the Save).
func TestProviderSaveModelPrefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	postTo := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w
	}
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		return postTo("/api/provider/save", body)
	}
	if w := post(`{"id":"relay","name":"Relay","key":"k","chat":"http://127.0.0.1:1/v1","models":["sol"],"new":true}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := post(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["sol"],
		"modelPrefs":{"sol":{"name":"My Sol","efforts":["low","high"],"images":true,"search":true}}}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s := settings.Load()
	if s.ModelNames["relay/sol"] != "My Sol" || !slices.Equal(s.ModelEfforts["relay/sol"], []string{"low", "high"}) || !s.ModelImages["relay/sol"] || !s.ModelSearches["relay/sol"] {
		t.Fatalf("names %v, efforts %v, images %v, searches %v", s.ModelNames, s.ModelEfforts, s.ModelImages, s.ModelSearches)
	}
	// its own answer back, as the editor's Restore default sends it
	if w := post(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["sol"],
		"modelPrefs":{"sol":{"ownSearch":true}}}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, held := settings.Load().ModelSearches["relay/sol"]; held {
		t.Fatal("the answer stayed")
	}
	// and the click-at-a-time action the row itself uses
	if w := postTo("/api/provider/search", `{"id":"relay","model":"sol","search":true}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !settings.Load().ModelSearches["relay/sol"] {
		t.Fatal("the action saved nothing")
	}
	// the model it is the same as, for the groups magpie finds (#583)
	if w := post(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["sol"],"modelPrefs":{"sol":{"same":"deepseek-v4.1-flash"}}}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := settings.Load().ModelSameAs["relay/sol"]; got != "deepseek-v4.1-flash" {
		t.Fatalf("same as %q", got)
	}
	// one it refuses fails the Save, saying why
	if w := post(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["sol"],"modelPrefs":{"sol":{"efforts":["loud"]}}}`); w.Code == 200 || !strings.Contains(w.Body.String(), "loud") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
