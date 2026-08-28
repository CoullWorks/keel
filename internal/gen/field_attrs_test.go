package gen

import (
	"strings"
	"testing"
)

// TestParseFieldAttrs covers the comma-attribute grammar added in the P0
// foundation: name:type[,nullable][,unique][,index][,default=..][,len=..] plus
// the Magento identity attributes, and confirms the legacy colon form still
// parses.
func TestParseFieldAttrs(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Field
		wantErr bool
	}{
		{"legacy still works", "title:string", Field{Name: "title", Type: TypeString}, false},
		{"legacy nullable colon", "note:text:nullable", Field{Name: "note", Type: TypeText, Nullable: true}, false},
		{"comma nullable", "note:text,nullable", Field{Name: "note", Type: TypeText, Nullable: true}, false},
		{"unique+index", "sku:string,unique,index", Field{Name: "sku", Type: TypeString, Unique: true, Index: true}, false},
		{"default value", "total:decimal,default=0", Field{Name: "total", Type: TypeDecimal, Default: "0"}, false},
		{"length", "title:string,len=120", Field{Name: "title", Type: TypeString, Length: 120}, false},
		{"length alias", "title:string,length=64", Field{Name: "title", Type: TypeString, Length: 64}, false},
		{"identity+unsigned", "entity_id:int,identity,unsigned", Field{Name: "entity_id", Type: TypeInt, Identity: true, Unsigned: true}, false},
		{"grid alias", "sku:string,grid", Field{Name: "sku", Type: TypeString, AddToGrid: true}, false},
		{"combined", "price:decimal,nullable,default=9.99,unique", Field{Name: "price", Type: TypeDecimal, Nullable: true, Default: "9.99", Unique: true}, false},
		{"empty attr tolerated", "x:int,", Field{Name: "x", Type: TypeInt}, false},
		{"unknown attr", "x:int,wibble", Field{}, true},
		{"unknown valued attr", "x:int,foo=bar", Field{}, true},
		{"bad length", "x:string,len=nope", Field{}, true},
		{"negative length", "x:string,len=-3", Field{}, true},
		{"missing type with comma", "x,index", Field{}, true},
		{"unknown type with attr", "x:blob,index", Field{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseField(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseField(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseField(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseField(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestFieldLengthRendersMagento proves an explicit varchar length overrides the
// default length="255" and unsigned/identity land in the column.
func TestFieldLengthRendersMagento(t *testing.T) {
	xml, err := MagentoColumnsXML([]Field{
		{Name: "title", Type: TypeString, Length: 120},
		{Name: "entity_id", Type: TypeInt, Identity: true, Unsigned: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `length="120"`) {
		t.Errorf("expected length=120 in %q", xml)
	}
	if strings.Contains(xml, `length="255"`) {
		t.Errorf("explicit length should replace the default 255: %q", xml)
	}
	if !strings.Contains(xml, `identity="true"`) {
		t.Errorf("expected identity=true in %q", xml)
	}
	if !strings.Contains(xml, `unsigned="true"`) {
		t.Errorf("expected unsigned=true in %q", xml)
	}
}

// TestFieldLengthRendersLaravel proves an explicit string length becomes the
// Blueprint string()'s second arg and an unsigned int uses unsignedInteger().
func TestFieldLengthRendersLaravel(t *testing.T) {
	lines, err := LaravelSchemaLines([]Field{
		{Name: "title", Type: TypeString, Length: 120},
		{Name: "count", Type: TypeInt, Unsigned: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lines, "$table->string('title', 120)") {
		t.Errorf("expected string('title', 120) in %q", lines)
	}
	if !strings.Contains(lines, "$table->unsignedInteger('count')") {
		t.Errorf("expected unsignedInteger('count') in %q", lines)
	}
}
