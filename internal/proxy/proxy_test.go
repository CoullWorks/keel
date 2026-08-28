package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectFor(t *testing.T) {
	cases := map[string]string{
		"myshop.localhost":             "myshop",
		"myshop.localhost:8080":        "myshop",
		"MyShop.LocalHost":             "myshop",
		"myshop.localhost.":            "myshop", // a browser may send the root dot
		"api.myshop.localhost":         "myshop", // a sub-subdomain still reaches the project
		"deep.api.myshop.localhost":    "myshop",
		"keel-e2e-laravel.localhost":   "keel-e2e-laravel",
		"example.com":                  "",
		"127.0.0.1:8080":               "",
		"localhost":                    "",
		"localhost:3000":               "",
		"notlocalhost.example":         "",
		"myshop.localhost.example.com": "",
	}
	for host, want := range cases {
		if got := ProjectFor(host); got != want {
			t.Errorf("ProjectFor(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestRoutesToTheRegisteredPort(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "served %s to host %s", r.URL.Path, r.Host)
	}))
	defer app.Close()

	table := NewTable()
	table.Set("myshop", portOf(t, app.URL))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://myshop.localhost/cart", nil)
	req.Host = "myshop.localhost"
	Handler(table).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "served /cart") {
		t.Errorf("path was not preserved: %s", body)
	}
	// Frameworks build absolute URLs and set cookies from Host, and Vite
	// rejects a request whose host it does not recognise.
	if !strings.Contains(body, "host myshop.localhost") {
		t.Errorf("the original Host was not forwarded: %s", body)
	}
}

func TestUnknownProjectListsWhatIsRunning(t *testing.T) {
	table := NewTable()
	table.Set("myshop", 4001)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://typo.localhost/", nil)
	req.Host = "typo.localhost"
	Handler(table).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown project, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "typo") {
		t.Errorf("the message should name what was asked for: %s", body)
	}
	// The whole point of the message: say which names do work.
	if !strings.Contains(body, "myshop.localhost") {
		t.Errorf("the message should list the running projects: %s", body)
	}
}

func TestNonLocalhostHostIsRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Host = "example.com"
	Handler(NewTable()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for a host this proxy does not serve, got %d", rec.Code)
	}
}

// A dev server that has stopped is the common case, and a bare 502 does not say
// what to do about it.
func TestDeadBackendExplainsItself(t *testing.T) {
	dead, err := FreePort()
	if err != nil {
		t.Skip("cannot allocate a port here")
	}
	table := NewTable()
	table.Set("myshop", dead)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://myshop.localhost/", nil)
	req.Host = "myshop.localhost"
	Handler(table).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502 when nothing answers, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "keel run dev") {
		t.Errorf("the message should say how to fix it: %s", rec.Body)
	}
}

func TestTableSetReplacesAPort(t *testing.T) {
	table := NewTable()
	table.Set("myshop", 3000)
	table.Set("myshop", 4100) // restarted on a different port
	if p, _ := table.Port("myshop"); p != 4100 {
		t.Errorf("want the newest port 4100, got %d", p)
	}
	table.Remove("myshop")
	if _, ok := table.Port("myshop"); ok {
		t.Error("Remove should unregister the project")
	}
}

func TestFreePortIsUsable(t *testing.T) {
	p, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p < 1024 {
		t.Errorf("want an unprivileged port, got %d", p)
	}
	// Allocation is only useful if the port is actually bindable afterwards.
	s, err := Listen(p, NewTable())
	if err != nil {
		t.Fatalf("the allocated port %d was not bindable: %v", p, err)
	}
	s.Close()
}

func TestEndToEndThroughARealListener(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "app is running")
	}))
	defer app.Close()

	table := NewTable()
	table.Set("myshop", portOf(t, app.URL))

	port, err := FreePort()
	if err != nil {
		t.Skip("cannot allocate a port here")
	}
	s, err := Listen(port, table)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go s.Serve()

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req.Host = "myshop.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request through the proxy failed: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "app is running" {
		t.Errorf("want the app's response, got %q", b)
	}
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(rawURL, "http://127.0.0.1:%d", &port); err != nil {
		t.Fatalf("cannot read the port from %s: %v", rawURL, err)
	}
	return port
}
