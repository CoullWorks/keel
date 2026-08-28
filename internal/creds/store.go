package creds

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/coullworks/keel/internal/atomicfile"
	"github.com/coullworks/keel/internal/profile"
	"gopkg.in/yaml.v3"
)

// Store is the credentials the user chose to remember, so a second Magento
// project does not ask for the same Adobe keys again.
//
// It is a separate file from the profile on purpose. The profile is ordinary
// settings that people copy between machines, paste into issues and check into
// dotfile repos; credentials are none of those things, and mixing them would
// make every future "share your profile" a leak.
type Store struct {
	Values []Value `yaml:"credentials"`
}

// Path is the credential file: <config>/credentials.yaml.
func Path() string { return filepath.Join(profile.Dir(), "credentials.yaml") }

// Load reads the remembered credentials. A missing file is an empty store, not
// an error: most people have never saved one.
func Load() (*Store, error) {
	s := &Store{}
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(), err)
	}
	return s, nil
}

// Save writes the store 0600, and repairs the permissions on an existing file
// that is more open than that.
func (s *Store) Save() error {
	sort.Slice(s.Values, func(i, j int) bool {
		if s.Values[i].Kind != s.Values[j].Kind {
			return s.Values[i].Kind < s.Values[j].Kind
		}
		return s.Values[i].ID < s.Values[j].ID
	})
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return err
	}
	// Atomic write: the credentials file must never be seen half-written, and the
	// temp-then-rename replaces any pre-existing file wholesale — so a file created
	// before the 0600 rule (or by something else) is left at 0600 by the rename,
	// with no separate chmod-repair needed.
	return atomicfile.WriteFile(Path(), b, 0o600)
}

// Get returns a remembered value by id.
func (s *Store) Get(id string) (Value, bool) {
	for _, v := range s.Values {
		if v.ID == id {
			return v, true
		}
	}
	return Value{}, false
}

// Remember adds or replaces a value. Values not marked Remember are dropped, so
// "do not save this one" also removes a previously saved copy rather than
// leaving a stale secret behind.
func (s *Store) Remember(values ...Value) {
	for _, v := range values {
		idx := -1
		for i := range s.Values {
			if s.Values[i].ID == v.ID {
				idx = i
				break
			}
		}
		if !v.Remember || !v.Filled() {
			if idx >= 0 {
				s.Values = append(s.Values[:idx], s.Values[idx+1:]...)
			}
			continue
		}
		v.Remember = false // not persisted; it describes the choice, not the value
		if idx >= 0 {
			s.Values[idx] = v
			continue
		}
		s.Values = append(s.Values, v)
	}
}

// Fill returns values for the requested credentials, taking anything already
// remembered. Callers ask the user only for what comes back unfilled.
func (s *Store) Fill(want []Value) []Value {
	out := make([]Value, 0, len(want))
	for _, w := range want {
		if w.Filled() {
			out = append(out, w)
			continue
		}
		if got, ok := s.Get(w.ID); ok {
			got.Kind = w.Kind
			out = append(out, got)
			continue
		}
		out = append(out, w)
	}
	return out
}
