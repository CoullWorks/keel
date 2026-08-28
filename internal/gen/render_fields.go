package gen

import (
	"fmt"
	"strings"
)

// magentoColumn is how one neutral FieldType maps onto a Magento
// db_schema.xml column: the xsi:type plus any type-specific attributes Magento
// needs (a varchar length, a decimal scale/precision). Kept as data so the
// mapping is one table to read rather than a switch scattered through a template.
type magentoColumn struct {
	xsiType string
	extra   string // extra attributes, e.g. length="255"
}

var magentoColumns = map[FieldType]magentoColumn{
	TypeString:    {"varchar", `length="255"`},
	TypeText:      {"text", `nullable="true"`},
	TypeInt:       {"int", `unsigned="false"`},
	TypeForeignID: {"int", `unsigned="true"`},
	TypeDecimal:   {"decimal", `scale="4" precision="12"`},
	TypeBool:      {"boolean", ""},
	TypeDateTime:  {"datetime", ""},
	TypeJSON:      {"text", ""}, // Magento has no native json column; text holds it
}

// MagentoColumnsXML renders the <column> (and, for unique fields, <constraint>)
// lines that go inside a db_schema.xml <table>, one per field, indented to sit
// under the entity_id primary key. It is the field-aware replacement for the
// single hardcoded entity_id column the old template emitted: a model with fields
// now produces real columns.
func MagentoColumnsXML(fields []Field) (string, error) {
	var cols, constraints strings.Builder
	for _, f := range fields {
		mc, ok := magentoColumns[f.Type]
		if !ok {
			return "", fmt.Errorf("magento: unsupported field type %q for %q", f.Type, f.Name)
		}
		attrs := []string{fmt.Sprintf(`xsi:type="%s"`, mc.xsiType), fmt.Sprintf(`name="%s"`, f.Name)}
		// An explicit Length on a varchar column overrides the type's default
		// length="255"; otherwise the type-default extra attributes apply.
		if f.Type == TypeString && f.Length > 0 {
			attrs = append(attrs, fmt.Sprintf(`length="%d"`, f.Length))
		} else if mc.extra != "" && f.Type != TypeText { // text already carries nullable
			attrs = append(attrs, mc.extra)
		}
		// Unsigned overrides the int type default for integer columns.
		if f.Unsigned && (f.Type == TypeInt || f.Type == TypeForeignID) {
			attrs = append(attrs, `unsigned="true"`)
		}
		// A field marked nullable overrides the type default; otherwise columns are
		// NOT NULL unless the type is inherently free-text.
		if f.Type != TypeText {
			attrs = append(attrs, fmt.Sprintf(`nullable="%t"`, f.Nullable))
		}
		if f.Identity {
			attrs = append(attrs, `identity="true"`)
		}
		if f.Default != "" {
			attrs = append(attrs, fmt.Sprintf(`default="%s"`, f.Default))
		}
		attrs = append(attrs, fmt.Sprintf(`comment="%s"`, f.Name))
		fmt.Fprintf(&cols, "        <column %s/>\n", strings.Join(attrs, " "))
		if f.Unique {
			fmt.Fprintf(&constraints, "        <constraint xsi:type=\"unique\" referenceId=\"%s_UNIQUE\">\n            <column name=\"%s\"/>\n        </constraint>\n", strings.ToUpper(f.Name), f.Name)
		}
		if f.Index {
			fmt.Fprintf(&constraints, "        <index referenceId=\"%s_INDEX\" indexType=\"btree\">\n            <column name=\"%s\"/>\n        </index>\n", strings.ToUpper(f.Name), f.Name)
		}
	}
	return cols.String() + constraints.String(), nil
}

// laravelMethod is the Blueprint method for a neutral FieldType, e.g. "string",
// "decimal". foreignId is special-cased in the renderer because it also adds a
// constrained() chain.
var laravelMethods = map[FieldType]string{
	TypeString:    "string",
	TypeInt:       "integer",
	TypeDecimal:   "decimal",
	TypeBool:      "boolean",
	TypeText:      "text",
	TypeDateTime:  "dateTime",
	TypeForeignID: "foreignId",
	TypeJSON:      "json",
}

// LaravelSchemaLines renders the `$table->...;` lines that go inside a migration
// Schema::create closure, one per field, honouring nullable/default/unique/index.
// It is the field-aware body the empty migration used to lack.
func LaravelSchemaLines(fields []Field) (string, error) {
	var b strings.Builder
	for _, f := range fields {
		method, ok := laravelMethods[f.Type]
		if !ok {
			return "", fmt.Errorf("laravel: unsupported field type %q for %q", f.Type, f.Name)
		}
		var line string
		switch f.Type {
		case TypeForeignID:
			line = fmt.Sprintf("$table->foreignId('%s')", f.Name)
		case TypeDecimal:
			line = fmt.Sprintf("$table->decimal('%s', 12, 4)", f.Name)
		case TypeString:
			// An explicit length becomes the Blueprint string()'s second argument.
			if f.Length > 0 {
				line = fmt.Sprintf("$table->string('%s', %d)", f.Name, f.Length)
			} else {
				line = fmt.Sprintf("$table->string('%s')", f.Name)
			}
		case TypeInt:
			// An unsigned integer uses the Blueprint unsignedInteger() method.
			if f.Unsigned {
				line = fmt.Sprintf("$table->unsignedInteger('%s')", f.Name)
			} else {
				line = fmt.Sprintf("$table->integer('%s')", f.Name)
			}
		default:
			line = fmt.Sprintf("$table->%s('%s')", method, f.Name)
		}
		if f.Nullable {
			line += "->nullable()"
		}
		if f.Default != "" {
			line += fmt.Sprintf("->default(%s)", laravelDefaultLiteral(f))
		}
		if f.Unique {
			line += "->unique()"
		}
		if f.Index {
			line += "->index()"
		}
		if f.Type == TypeForeignID {
			line += "->constrained()"
		}
		fmt.Fprintf(&b, "            %s;\n", line)
	}
	return b.String(), nil
}

// laravelDefaultLiteral renders a field's default as a PHP literal: numbers and
// booleans bare, everything else quoted, so a string default is a valid PHP
// string rather than a bareword.
func laravelDefaultLiteral(f Field) string {
	switch f.Type {
	case TypeInt, TypeDecimal, TypeForeignID:
		return f.Default
	case TypeBool:
		switch strings.ToLower(f.Default) {
		case "1", "true":
			return "true"
		default:
			return "false"
		}
	default:
		return "'" + strings.ReplaceAll(f.Default, "'", "\\'") + "'"
	}
}

// LaravelFillable renders the $fillable array body — the quoted, comma-joined
// field names — so a generated model exposes its columns for mass assignment
// instead of shipping an empty guard.
func LaravelFillable(fields []Field) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = "'" + f.Name + "'"
	}
	return strings.Join(names, ", ")
}
