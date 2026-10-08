package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// Whether a model searches the web by itself is the user's to say, by
// "<provider>/<model>" and by "<provider>/*" for every model of a provider
// (settings' ModelSearches), over what magpie knows of the vendor: a model
// said not to hands the search to magpie's searcher, and one said to is
// asked as sent on the APIs a search tool goes out on.
func TestModelSearchAnswerBeatsTheRules(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	setHome(t, t.TempDir())
	// the models.dev entries the two providers' lists are read from
	os.MkdirAll(filepath.Dir(catalog.CachePath()), 0o755)
	os.WriteFile(catalog.CachePath(), []byte(`{"relaycat":{"models":{"m":{"id":"m"}}},
		"plaincat":{"models":{"m":{"id":"m"},"other":{"id":"other"}}}}`), 0o644)
	catalog.Reset()
	t.Cleanup(catalog.Reset)
	// a relay in front of Anthropic's API, which searches by itself
	relay := provider.Provider{ID: "relay", Name: "Relay", Key: "k", Catalog: "relaycat", Searches: true, Anthropic: "https://relay.example/v1"}
	if err := provider.Save(relay); err != nil {
		t.Fatal(err)
	}
	p, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	if !SearchesByItself(*p, "m") || !searchesModel(*p, provider.Anthropic, "m") {
		t.Fatal("the relay's own answer is not that it searches")
	}
	no, yes := false, true
	if err := provider.SetModelSearch("relay/m", &no); err != nil {
		t.Fatal(err)
	}
	if SearchesByItself(*p, "m") || searchesItselfFor(*p, provider.Anthropic, "m") || searchesModel(*p, provider.Anthropic, "m") || searchesFor(*p, provider.Anthropic, "m", &Request{WebSearch: true}) {
		t.Fatal("a model said not to search still does")
	}
	if pref := ModelSearch(*p, "m"); !pref.Set || pref.Searches || !pref.Own {
		t.Fatalf("preference %+v", pref)
	}
	// another magpie is told magpie searches for it, not that its vendor does
	if got := webSearchOf(provider.Entry{Provider: *p, Model: "m"}, true); got != searchMagpie {
		t.Fatalf("advertised %q", got)
	}
	if searchableModel(*p, "m") {
		t.Fatal("a model said not to search was offered as a searcher")
	}
	if err := provider.SetModelSearch("relay/m", nil); err != nil {
		t.Fatal(err)
	}
	if !SearchesByItself(*p, "m") {
		t.Fatal("the relay's own answer did not come back")
	}

	// a provider magpie's own rules know nothing about: the user's answer
	// is what makes it search, where an API takes a search tool
	plain := provider.Provider{ID: "plain", Name: "Plain", Key: "k", Catalog: "plaincat", Responses: "https://plain.example/v1"}
	if err := provider.Save(plain); err != nil {
		t.Fatal(err)
	}
	pp, err := provider.Find("plain")
	if err != nil {
		t.Fatal(err)
	}
	if SearchesByItself(*pp, "m") {
		t.Fatal("plain searches by itself before anything was said")
	}
	if err := provider.SetModelSearch("plain/*", &yes); err != nil {
		t.Fatal(err)
	}
	if !SearchesByItself(*pp, "m") || !SearchesByItself(*pp, "other") || !searchesModel(*pp, provider.Responses, "m") {
		t.Fatal("the provider-wide answer does not apply to every model")
	}
	// the tool only goes out on an API that takes it
	if searchesModel(*pp, provider.Chat, "m") {
		t.Fatal("a search went out on a Chat API")
	}
	// the model's own answer beats the provider's
	if err := provider.SetModelSearch("plain/m", &no); err != nil {
		t.Fatal(err)
	}
	if SearchesByItself(*pp, "m") || !SearchesByItself(*pp, "other") {
		t.Fatal("the model's own answer did not beat the provider's")
	}
	if v, ok := provider.SearchOverride("plain", "m"); !ok || v {
		t.Fatalf("override %v %v", v, ok)
	}
}

func TestModelSearchRestoresTheDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	setHome(t, t.TempDir())
	p := provider.Provider{ID: "relay", Searches: true, Anthropic: "https://relay.example/v1"}
	for _, c := range []struct {
		name     string
		answers  map[string]bool
		searches bool
		own      bool
		set      bool
	}{
		{"native default", nil, true, true, false},
		{"native turned off", map[string]bool{"relay/m": false}, false, true, true},
		{"inherited off", map[string]bool{"relay/*": false}, false, false, false},
		{"model beats inherited off", map[string]bool{"relay/*": false, "relay/m": true}, true, false, true},
		{"model beats inherited on", map[string]bool{"relay/*": true, "relay/m": false}, false, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := settings.Load()
			s.ModelSearches = c.answers
			if err := settings.Save(s); err != nil {
				t.Fatal(err)
			}
			pref := ModelSearch(p, "m")
			if pref.Searches != c.searches || pref.Own != c.own || pref.Set != c.set {
				t.Fatalf("preference %+v, want searches %v, Restore default %v, model answer set %v", pref, c.searches, c.own, c.set)
			}
		})
	}
}

func TestChosenSearcherSkipsModelsSaidNotToSearch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	setHome(t, t.TempDir())
	catalog.Reset()
	t.Cleanup(catalog.Reset)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"m1"},{"id":"safe-lite"}]}`)
	}))
	t.Cleanup(relay.Close)
	p := provider.Provider{ID: "relay", Name: "Relay", Key: "k", Searches: true, Anthropic: relay.URL}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	// safe-lite is left out of the small-model heuristic, but can still
	// be used by the final fallback. m1 is never eligible.
	for _, ref := range []string{"relay", "relay/m1", "relay/missing"} {
		for _, blocked := range []bool{false, true} {
			s := settings.Load()
			s.Searcher = ref
			s.ModelSearches = map[string]bool{"relay/m1": false, "relay/safe-lite": !blocked}
			if err := settings.Save(s); err != nil {
				t.Fatal(err)
			}
			wantModel, wantWhy := "safe-lite", ""
			if blocked {
				wantModel, wantWhy = "", SearcherNoneOf
			}
			got, model, why := chosenSearcher()
			if got == nil || got.ID != p.ID || model != wantModel || why != wantWhy {
				t.Errorf("%s, all blocked %v: searcher %v/%s (%s), want %s (%s)", ref, blocked, got, model, why, wantModel, wantWhy)
			}
			if blocked {
				if chosen, model, ok := searcher(); ok {
					t.Errorf("%s: a model said not to search was still chosen: %s/%s", ref, chosen.ID, model)
				}
			}
		}
	}
}

func TestModelSearchNeedsASearchAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	setHome(t, t.TempDir())
	for _, c := range []struct {
		name     string
		p        provider.Provider
		proto    provider.Protocol
		native   bool
		own      bool
		otherAPI bool
	}{
		{"unknown Chat", provider.Provider{Chat: "https://relay.example/v1"}, provider.Chat, false, false, false},
		{"unknown Anthropic", provider.Provider{Anthropic: "https://relay.example"}, provider.Anthropic, true, false, false},
		{"unknown Responses", provider.Provider{Responses: "https://relay.example/v1"}, provider.Responses, true, false, false},
		{"OpenRouter Chat", provider.Provider{Chat: "https://openrouter.ai/api/v1"}, provider.Chat, true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := c.p
			p.ID = "relay"
			s := settings.Load()
			s.ModelSearches = map[string]bool{"relay/m": true}
			if err := settings.Save(s); err != nil {
				t.Fatal(err)
			}
			if pref := ModelSearch(p, "m"); !pref.Searches || !pref.Set || pref.Own != c.own || pref.OtherAPI != c.otherAPI {
				t.Fatalf("editor preference %+v, want saved on, default %v, other API %v", pref, c.own, c.otherAPI)
			}
			if got := SearchesByItself(p, "m"); got != c.native {
				t.Errorf("searches by itself = %v, want %v", got, c.native)
			}
			if got := searchesModel(p, c.proto, "m"); got != c.native {
				t.Errorf("takes a vendor search on %s = %v, want %v", c.proto, got, c.native)
			}
		})
	}
}

// A client's web search, with a searcher set up: a model the user says
// searches by itself is asked as sent, and one they say doesn't is asked
// through magpie's searcher instead, its vendor's own tool left out.
func TestModelSearchAnswerRoutesTheRequest(t *testing.T) {
	var mu sync.Mutex
	var asked []string // what the relay was sent
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"data":[{"id":"m-1"}]}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		asked = append(asked, string(b))
		mu.Unlock()
		if strings.Contains(string(b), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, sse(`event: message_start`+"\n"+`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m-1","content":[],"usage":{"input_tokens":5}}}`,
				`event: content_block_start`+"\n"+`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Go 1.27.1 is out."}}`,
				`event: content_block_stop`+"\n"+`data: {"type":"content_block_stop","index":0}`,
				`event: message_delta`+"\n"+`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
				`event: message_stop`+"\n"+`data: {"type":"message_stop"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"m-1","content":[{"type":"text","text":"Go 1.27.1 is out."}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":5}}`)
	}))
	defer relay.Close()
	// a searcher magpie would use for a model that can't search, which is
	// what makes the difference visible: without it there is no one to
	// search with and the vendor is asked as sent either way
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"data":[{"id":"claude-haiku-4-5"}]}`)
			return
		}
		t.Errorf("the searcher was asked: %s %s", r.URL.Path, r.Method)
		http.NotFound(w, r)
	}))
	defer search.Close()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	setHome(t, t.TempDir())
	hosts := searchHosts[provider.Anthropic]
	searchHosts[provider.Anthropic] = append(hosts, provider.HostOf(search.URL))
	defer func() { searchHosts[provider.Anthropic] = hosts }()
	for _, p := range []provider.Provider{
		{ID: "srch", Name: "Search", Key: "k", Anthropic: search.URL},
		{ID: "relay", Name: "Relay", Key: "k", Anthropic: relay.URL},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
		q, _ := provider.Find(p.ID)
		if _, err := q.Fetch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"model":"relay/m-1","max_tokens":1000,"stream":false,"messages":[{"role":"user","content":"What is the latest Go?"}],
		"tools":[{"name":"Read","description":"read","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`
	ask := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		New().Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		mu.Lock()
		defer mu.Unlock()
		last := asked[len(asked)-1]
		asked = nil
		return last
	}

	// said to search: the vendor's own tool goes out as the client sent it
	yes := true
	if err := provider.SetModelSearch("relay/m-1", &yes); err != nil {
		t.Fatal(err)
	}
	if sent := ask(); !strings.Contains(sent, `"web_search_20250305"`) {
		t.Fatalf("the vendor's tool was not sent as is: %s", sent)
	}
	// said not to: magpie searches for it, so the model is asked with
	// magpie's own web_search tool instead
	if err := provider.SetModelSearch("relay/m-1", nil); err != nil {
		t.Fatal(err)
	}
	if sent := ask(); strings.Contains(sent, `"web_search_20250305"`) || !strings.Contains(sent, `"name":"web_search"`) {
		t.Fatalf("the search was not magpie's: %s", sent)
	}
}
