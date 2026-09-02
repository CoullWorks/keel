// Package envfile parses, merges and writes .env files without a dependency,
// so keel can sync .env against .env.example and audit for missing/empty secrets.
package envfile

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Pair is one KEY=value line in declaration order.
type Pair struct {
	Key   string
	Value string
}

// File is an ordered set of key/value pairs preserving first-seen order.
type File struct {
	Pairs []Pair
	index map[string]int
}

// Parse reads dotenv content. It ignores blank lines and # comments, trims an
// optional leading `export `, and strips one layer of surrounding quotes.
func Parse(b []byte) *File {
	f := &File{index: map[string]int{}}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 1 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		f.set(key, val)
	}
	return f
}

// Load parses a file from disk. A missing file yields an empty File, no error.
func Load(path string) (*File, error) {
	// The caller supplies the full .env path; normalise it and refuse a traversal.
	p, err := filepath.Abs(path)
	if err != nil || strings.Contains(p, "..") {
		return nil, fmt.Errorf("refusing path outside %q", path)
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &File{index: map[string]int{}}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b), nil
}

func (f *File) set(key, val string) {
	if i, ok := f.index[key]; ok {
		f.Pairs[i].Value = val
		return
	}
	f.index[key] = len(f.Pairs)
	f.Pairs = append(f.Pairs, Pair{Key: key, Value: val})
}

// Set upserts a key (exported for recipe patches that own .env joins).
func (f *File) Set(key, val string) {
	if f.index == nil {
		f.index = map[string]int{}
	}
	f.set(key, val)
}

// Has reports whether the key exists.
func (f *File) Has(key string) bool { _, ok := f.index[key]; return ok }

// Get returns the value (empty if absent).
func (f *File) Get(key string) string {
	if i, ok := f.index[key]; ok {
		return f.Pairs[i].Value
	}
	return ""
}

// Keys returns keys in declaration order.
func (f *File) Keys() []string {
	ks := make([]string, len(f.Pairs))
	for i, p := range f.Pairs {
		ks[i] = p.Key
	}
	return ks
}

// MissingFrom returns keys present in example but absent from f (drift).
func (f *File) MissingFrom(example *File) []string {
	var out []string
	for _, k := range example.Keys() {
		if !f.Has(k) {
			out = append(out, k)
		}
	}
	return out
}

// EmptyKeys returns keys whose value is blank or a placeholder (needs filling).
func (f *File) EmptyKeys() []string {
	var out []string
	for _, p := range f.Pairs {
		if isPlaceholder(p.Value) {
			out = append(out, p.Key)
		}
	}
	sort.Strings(out)
	return out
}

func isPlaceholder(v string) bool {
	if strings.TrimSpace(v) == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "changeme", "change-me", "todo", "xxx", "secret", "your-secret", "your_secret_here":
		return true
	}
	return false
}

// Merge fills keys from example that are missing in f, using the example's value
// as the seed. Existing values in f are never overwritten. Returns added keys.
func (f *File) Merge(example *File) []string {
	var added []string
	for _, p := range example.Pairs {
		if !f.Has(p.Key) {
			f.set(p.Key, p.Value)
			added = append(added, p.Key)
		}
	}
	return added
}

// Render serialises the file back to dotenv text in declaration order.
func (f *File) Render() string {
	var b strings.Builder
	for _, p := range f.Pairs {
		fmt.Fprintf(&b, "%s=%s\n", p.Key, p.Value)
	}
	return b.String()
}

// Secret returns a URL-safe random secret of ~nbytes entropy.
func Secret(nbytes int) (string, error) {
	if nbytes < 16 {
		nbytes = 16
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
