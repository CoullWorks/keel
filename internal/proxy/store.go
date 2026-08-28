package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/coullworks/keel/internal/profile"
)

// The proxy and the dev servers are separate processes, so the routing table
// has to survive on disk. It is state about what is running now, not
// configuration, which is why it records a pid: an entry whose process is gone
// is stale and must not be served.

// Entry is a running project as recorded on disk.
type Entry struct {
	Name    string    `yaml:"name"`
	Port    int       `yaml:"port"`
	PID     int       `yaml:"pid"`
	Dir     string    `yaml:"dir,omitempty"`
	Started time.Time `yaml:"started"`
}

// URL is where a browser reaches this project.
func (e Entry) URL(proxyPort int) string {
	if proxyPort == 80 {
		return fmt.Sprintf("http://%s%s/", e.Name, Suffix)
	}
	return fmt.Sprintf("http://%s%s:%d/", e.Name, Suffix, proxyPort)
}

type file struct {
	Entries []Entry `yaml:"running"`
}

func storePath() string { return filepath.Join(profile.Dir(), "running.yaml") }

// Load returns the entries whose process is still alive.
//
// Pruning on read rather than trusting the file is deliberate: a dev server
// killed with SIGKILL, or a machine that lost power, never gets to deregister
// itself, and a stale entry would route a name at a port something else has
// since been given.
func Load() ([]Entry, error) {
	b, err := os.ReadFile(storePath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", storePath(), err)
	}
	live := make([]Entry, 0, len(f.Entries))
	for _, e := range f.Entries {
		if alive(e.PID) {
			live = append(live, e)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Name < live[j].Name })
	return live, nil
}

func save(entries []Entry) error {
	if err := os.MkdirAll(profile.Dir(), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(file{Entries: entries})
	if err != nil {
		return err
	}
	// Written via a temp file and renamed, so a proxy reading concurrently sees
	// either the old table or the new one, never half of one.
	tmp := storePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, storePath())
}

// Register publishes a project. A second run of the same project replaces the
// first: a restarted dev server should keep its name.
func Register(name string, port, pid int, dir string) error {
	entries, err := Load()
	if err != nil {
		return err
	}
	name = strings.ToLower(name)
	out := entries[:0:0]
	for _, e := range entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	out = append(out, Entry{Name: name, Port: port, PID: pid, Dir: dir, Started: time.Now()})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return save(out)
}

// Deregister removes a project, for a dev server shutting down cleanly.
func Deregister(name string) error {
	entries, err := Load()
	if err != nil {
		return err
	}
	name = strings.ToLower(name)
	out := entries[:0:0]
	for _, e := range entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	return save(out)
}

// LoadTable builds a routing table from what is running.
func LoadTable() (*Table, error) {
	entries, err := Load()
	if err != nil {
		return nil, err
	}
	t := NewTable()
	for _, e := range entries {
		t.Set(e.Name, e.Port)
	}
	return t, nil
}

// alive reports whether a pid is a live process. Signal 0 performs the
// permission and existence checks without delivering anything.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
