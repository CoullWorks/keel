// Package proxy routes <project>.localhost to whichever port that project's dev
// server happens to be listening on.
//
// The point is that the port stops mattering. A fixed port is a shared resource
// on a developer's machine: two projects both wanting 3000 collide, and so does
// anything else already holding it, including a process on the Windows side of
// WSL2 that Linux tooling cannot even see. Naming the project instead removes
// the collision entirely.
//
// `.localhost` is reserved for loopback by RFC 6761, so `myshop.localhost`
// resolves to 127.0.0.1 in browsers and curl with no hosts file to edit and no
// DNS to run. DDEV already does the same thing for its own projects with
// `.ddev.site`, so a DDEV project is left alone: it has a router already.
package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Suffix is the host suffix a project is reached on.
const Suffix = ".localhost"

// Route is one project and the port it is currently served from.
type Route struct {
	Name string
	Port int
}

// Table is the live name-to-port mapping. It is read on every request and
// rewritten whenever a project starts or stops, so it is guarded.
type Table struct {
	mu     sync.RWMutex
	routes map[string]int
}

func NewTable() *Table {
	return &Table{routes: map[string]int{}}
}

// Set publishes a project at a port. Replacing an existing entry is normal: a
// dev server restarted on a new port should be reachable at the same name.
func (t *Table) Set(name string, port int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes[strings.ToLower(name)] = port
}

func (t *Table) Remove(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.routes, strings.ToLower(name))
}

func (t *Table) Port(name string) (int, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.routes[strings.ToLower(name)]
	return p, ok
}

// Routes returns the table sorted by name, for `keel proxy status`.
func (t *Table) Routes() []Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Route, 0, len(t.routes))
	for n, p := range t.routes {
		out = append(out, Route{Name: n, Port: p})
	}
	return out
}

// ProjectFor extracts the project name from a Host header.
//
// The port is stripped because a browser sends "myshop.localhost:8080" when the
// proxy is not on 80, and a sub-subdomain keeps only its first label so
// "api.myshop.localhost" still reaches myshop.
func ProjectFor(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if !strings.HasSuffix(host, Suffix) {
		return ""
	}
	name := strings.TrimSuffix(host, Suffix)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// Handler routes by Host. Anything it cannot place gets an explanation rather
// than a bare 502, because "which name did you mean" is the only useful thing
// to say to someone who mistyped a project.
func Handler(t *Table) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			port, _ := t.Port(ProjectFor(r.In.Host))
			target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
			r.SetURL(target)
			// The dev server needs the original host: frameworks generate
			// absolute URLs and set cookies from it, and Vite refuses a request
			// whose host it does not recognise.
			r.Out.Host = r.In.Host
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			name := ProjectFor(r.Host)
			port, _ := t.Port(name)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "keel: %s is registered on port %d but nothing answered there.\n\n"+
				"The dev server has probably stopped. Start it with: keel run dev\n\n%v\n",
				name, port, err)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := ProjectFor(r.Host)
		if name == "" {
			writePlain(w, http.StatusNotFound,
				fmt.Sprintf("keel proxy serves %s names.\n\nGot Host: %s\n", Suffix, r.Host))
			return
		}
		if _, ok := t.Port(name); !ok {
			writePlain(w, http.StatusNotFound, unknownProject(name, t))
			return
		}
		rp.ServeHTTP(w, r)
	})
}

func unknownProject(name string, t *Table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "keel: no project named %q is running.\n\n", name)
	routes := t.Routes()
	if len(routes) == 0 {
		b.WriteString("Nothing is registered. Start a project with: keel run dev\n")
		return b.String()
	}
	b.WriteString("Running now:\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "  http://%s%s -> 127.0.0.1:%d\n", r.Name, Suffix, r.Port)
	}
	return b.String()
}

func writePlain(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

// Server is the proxy listening on one port.
type Server struct {
	Table *Table
	http  *http.Server
	ln    net.Listener
}

// Listen binds the proxy. Port 80 is what makes a bare hostname work, but it is
// privileged on Linux, so a caller that cannot bind it is told exactly that
// rather than being handed a permission error to interpret.
func Listen(port int, t *Table) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("keel proxy cannot bind 127.0.0.1:%d: %w", port, err)
	}
	s := &Server{
		Table: t,
		ln:    ln,
		http: &http.Server{
			Handler:           Handler(t),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
	return s, nil
}

func (s *Server) Addr() string { return s.ln.Addr().String() }

func (s *Server) Serve() error {
	if err := s.http.Serve(s.ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Close() error { return s.http.Close() }

// FreePort asks the kernel for an unused port.
//
// Allocating rather than hardcoding is the other half of this: with a proxy in
// front, nothing needs to know or care which port a dev server got, so there is
// no reason to fight over a well-known one.
func FreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
