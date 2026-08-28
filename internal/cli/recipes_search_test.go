package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubSearchDo swaps searchDo for the duration of a test. The passed func gets
// the real request and may hit an httptest server or return canned data.
func stubSearchDo(t *testing.T, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	old := searchDo
	searchDo = fn
	t.Cleanup(func() { searchDo = old })
}

// serveJSON stands up a throwaway server returning body with the given status,
// and points searchDo at it (preserving the query the command built).
func serveJSON(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	stubSearchDo(t, func(req *http.Request) (*http.Response, error) {
		// Re-point the request at the test server, keeping method/headers/ctx.
		u, err := req.URL.Parse(srv.URL)
		if err != nil {
			return nil, err
		}
		req.URL = u
		return http.DefaultClient.Do(req)
	})
}

// A successful search renders each pack with stars, name, description and the
// add hint.
func TestRecipesSearchSuccess(t *testing.T) {
	isolate(t)
	body := `{"items":[
		{"full_name":"acme/keel-shop","description":"Shop recipes","stargazers_count":42},
		{"full_name":"bob/keel-extras","description":"Extras","stargazers_count":7}
	]}`
	serveJSON(t, http.StatusOK, body)
	out, err := runRoot(t, "recipes", "search", "shop")
	if err != nil {
		t.Fatalf("recipes search: %v", err)
	}
	mustContain(t, out,
		"acme/keel-shop", "Shop recipes", "42",
		"bob/keel-extras",
		"keel recipes add acme/keel-shop",
	)
}

// Zero results prints the publish hint rather than erroring.
func TestRecipesSearchEmpty(t *testing.T) {
	isolate(t)
	serveJSON(t, http.StatusOK, `{"items":[]}`)
	out, err := runRoot(t, "recipes", "search")
	if err != nil {
		t.Fatalf("recipes search (empty): %v", err)
	}
	mustContain(t, out, "No packs found")
}

// A non-200 with an undecodable body surfaces a decode error (the command reads
// the body regardless of status). Proves the error branch is wired.
func TestRecipesSearchNon200BadBody(t *testing.T) {
	isolate(t)
	serveJSON(t, http.StatusForbidden, "not json at all")
	if _, err := runRoot(t, "recipes", "search", "x"); err == nil {
		t.Fatal("expected a decode error on a non-JSON body")
	}
}

// A transport error (client.Do fails) is wrapped with "searching GitHub".
func TestRecipesSearchTransportError(t *testing.T) {
	isolate(t)
	stubSearchDo(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	_, err := runRoot(t, "recipes", "search", "x")
	if err == nil {
		t.Fatal("expected a transport error")
	}
	mustContain(t, err.Error(), "searching GitHub")
}
