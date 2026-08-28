package gen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FieldType is a framework-neutral column type. keel owns one small vocabulary
// so a single `fields:` list renders into every backend: Magento's db_schema.xml
// column xsi:type, a Laravel migration `$table->...` method, and (later) Django
// model fields and SQLAlchemy columns. Each framework's renderer maps this
// neutral type onto its own, so a recipe author (and the studio's field table)
// picks from one list rather than learning each framework's DDL.
type FieldType string

const (
	TypeString    FieldType = "string"
	TypeInt       FieldType = "int"
	TypeDecimal   FieldType = "decimal"
	TypeBool      FieldType = "bool"
	TypeText      FieldType = "text"
	TypeDateTime  FieldType = "datetime"
	TypeForeignID FieldType = "foreignId"
	TypeJSON      FieldType = "json"
)

// FieldTypes is the ordered, canonical list of the supported field types. It
// feeds help text, the "unknown type" error and the studio's type dropdown, so
// there is one authoritative source rather than a list per surface.
var FieldTypes = []FieldType{
	TypeString, TypeInt, TypeDecimal, TypeBool, TypeText, TypeDateTime, TypeForeignID, TypeJSON,
}

// fieldTypeSet is FieldTypes as a lookup, built once.
var fieldTypeSet = func() map[FieldType]bool {
	m := make(map[FieldType]bool, len(FieldTypes))
	for _, t := range FieldTypes {
		m[t] = true
	}
	return m
}()

// FieldTypeStrings returns the field type names as plain strings, for joining
// into help and error text.
func FieldTypeStrings() []string {
	out := make([]string, len(FieldTypes))
	for i, t := range FieldTypes {
		out[i] = string(t)
	}
	return out
}

// Field is one typed column on a generated model. It is the mage2gen primitive:
// the same declarative row drives a Magento db_schema column, a Laravel migration
// line and the model's $fillable, so a "model" produces real columns instead of a
// single hardcoded entity_id. The attribute set is framework-neutral — a
// renderer takes only what its framework supports (Laravel ignores Identity;
// Magento maps AddToGrid into its UI component) — so one Field row is authored
// once and generates the same way everywhere.
type Field struct {
	Name     string    `yaml:"name"`
	Type     FieldType `yaml:"type"`
	Nullable bool      `yaml:"nullable"`
	Default  string    `yaml:"default"` // rendered literally; empty = no default
	Unique   bool      `yaml:"unique"`
	Index    bool      `yaml:"index"`
	Length   int       `yaml:"length"` // column length (0 = the type's default)
	// Identity/Unsigned/AddToGrid are the Magento identity attributes. Identity
	// marks an auto-increment column, Unsigned an unsigned integer, and AddToGrid
	// records that a field should appear in the admin grid. They are neutral on the
	// Field so every framework reads the one primitive; renderers that have no
	// concept for one simply ignore it.
	Identity  bool `yaml:"identity"`
	Unsigned  bool `yaml:"unsigned"`
	AddToGrid bool `yaml:"addToGrid"`
}

// fieldNamePattern is what a column may be called: a snake/camel identifier. A
// field name becomes a database column, an XML attribute value and a PHP/JS
// property, so — exactly like a component name — anything a shell, a filesystem
// or an XML parser could reinterpret is refused here.
var fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate checks a field is well-formed: a plain-identifier name and a known
// type. Fields reach keel from the CLI (`--field`), from recipe YAML and, through
// the studio, from a form, so they are validated once here rather than defused at
// each renderer.
func (f Field) Validate() error {
	if f.Name == "" {
		return fmt.Errorf("field name is empty")
	}
	if !fieldNamePattern.MatchString(f.Name) {
		return fmt.Errorf("invalid field name %q: use letters, digits and underscores", f.Name)
	}
	if !fieldTypeSet[f.Type] {
		return fmt.Errorf("unknown field type %q for %q: use one of %s", f.Type, f.Name, strings.Join(FieldTypeStrings(), ", "))
	}
	if f.Length < 0 {
		return fmt.Errorf("field %q has negative length %d", f.Name, f.Length)
	}
	return nil
}

// ValidateFields checks a whole list and reports the first problem, so a bad
// --field is caught before any file is written.
func ValidateFields(fs []Field) error {
	seen := map[string]bool{}
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			return err
		}
		if seen[f.Name] {
			return fmt.Errorf("duplicate field %q", f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// ParseField parses one CLI --field value into a Field. The grammar is
//
//	name:type[,attr][,attr=value]...
//
// where the type is followed by comma-separated attributes: the boolean flags
// nullable, unique, index, identity, unsigned and grid (an alias for
// "add to grid"), and the valued attributes default=<literal> and len=<n>. So
//
//	title:string,index,len=120
//	total:decimal,default=0
//	sku:string,unique,grid
//
// all parse. The legacy "name:type:nullable" colon form is still accepted so the
// common quick case and any existing scripts keep working: if the value has no
// comma but a third colon segment, it is read the old way.
func ParseField(s string) (Field, error) {
	head, attrs, hasAttrs := strings.Cut(s, ",")
	// No comma: could be the bare "name:type" or the legacy "name:type:nullable".
	if !hasAttrs {
		return parseLegacyField(s)
	}
	name, typ, ok := strings.Cut(head, ":")
	if !ok {
		return Field{}, fmt.Errorf("invalid --field %q: use name:type[,attr]...", s)
	}
	f := Field{Name: strings.TrimSpace(name), Type: FieldType(strings.TrimSpace(typ))}
	for _, a := range strings.Split(attrs, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		key, val, valued := strings.Cut(a, "=")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if err := applyFieldAttr(&f, key, val, valued, s); err != nil {
			return Field{}, err
		}
	}
	if err := f.Validate(); err != nil {
		return Field{}, err
	}
	return f, nil
}

// applyFieldAttr sets one parsed attribute on f, rejecting unknown or malformed
// attributes with the whole --field value for context.
func applyFieldAttr(f *Field, key, val string, valued bool, whole string) error {
	switch key {
	case "nullable", "null":
		f.Nullable = true
	case "unique":
		f.Unique = true
	case "index":
		f.Index = true
	case "identity":
		f.Identity = true
	case "unsigned":
		f.Unsigned = true
	case "grid", "add_to_grid":
		f.AddToGrid = true
	case "default":
		f.Default = val
	case "len", "length":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid --field %q: len= wants a non-negative number, got %q", whole, val)
		}
		f.Length = n
	default:
		if valued {
			return fmt.Errorf("invalid --field %q: unknown attribute %q=%q", whole, key, val)
		}
		return fmt.Errorf("invalid --field %q: unknown attribute %q", whole, key)
	}
	return nil
}

// parseLegacyField handles the comma-free forms: "name:type" and the historical
// "name:type:nullable".
func parseLegacyField(s string) (Field, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Field{}, fmt.Errorf("invalid --field %q: use name:type[,attr]... or name:type:nullable", s)
	}
	f := Field{Name: strings.TrimSpace(parts[0]), Type: FieldType(strings.TrimSpace(parts[1]))}
	if len(parts) == 3 {
		switch strings.TrimSpace(parts[2]) {
		case "nullable", "null":
			f.Nullable = true
		case "":
			// tolerate a trailing colon
		default:
			return Field{}, fmt.Errorf("invalid --field %q: the third part must be 'nullable' if present (or use name:type,attr...)", s)
		}
	}
	if err := f.Validate(); err != nil {
		return Field{}, err
	}
	return f, nil
}

// ParseFields parses every --field value, preserving order.
func ParseFields(ss []string) ([]Field, error) {
	out := make([]Field, 0, len(ss))
	for _, s := range ss {
		f, err := ParseField(s)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := ValidateFields(out); err != nil {
		return nil, err
	}
	return out, nil
}
