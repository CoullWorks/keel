package gen

import (
	"strings"
	"testing"
)

func TestParseField(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Field
		wantErr bool
	}{
		{"name and type", "title:string", Field{Name: "title", Type: TypeString}, false},
		{"nullable third part", "note:text:nullable", Field{Name: "note", Type: TypeText, Nullable: true}, false},
		{"null alias", "note:text:null", Field{Name: "note", Type: TypeText, Nullable: true}, false},
		{"foreignId", "author_id:foreignId", Field{Name: "author_id", Type: TypeForeignID}, false},
		{"trailing colon tolerated", "n:int:", Field{Name: "n", Type: TypeInt}, false},
		{"too few parts", "title", Field{}, true},
		{"too many parts", "a:b:c:d", Field{}, true},
		{"unknown type", "title:blob", Field{}, true},
		{"bad third part", "title:string:index", Field{}, true},
		{"bad name", "1bad:string", Field{}, true},
		{"empty name", ":string", Field{}, true},
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

func TestParseFieldsRejectsDuplicate(t *testing.T) {
	if _, err := ParseFields([]string{"title:string", "title:text"}); err == nil {
		t.Fatal("expected a duplicate-field error")
	}
}

func TestValidateFieldUnknownType(t *testing.T) {
	if err := (Field{Name: "x", Type: "widget"}).Validate(); err == nil {
		t.Fatal("expected unknown type to be rejected")
	}
}

// TestMagentoColumnsXML checks each neutral type renders to the right Magento
// column xsi:type, and that nullable/unique/index are honoured.
func TestMagentoColumnsXML(t *testing.T) {
	fields := []Field{
		{Name: "title", Type: TypeString},
		{Name: "body", Type: TypeText},
		{Name: "views", Type: TypeInt, Nullable: true},
		{Name: "price", Type: TypeDecimal},
		{Name: "active", Type: TypeBool, Default: "1"},
		{Name: "published_at", Type: TypeDateTime},
		{Name: "author_id", Type: TypeForeignID, Index: true},
		{Name: "meta", Type: TypeJSON},
		{Name: "slug", Type: TypeString, Unique: true},
	}
	out, err := MagentoColumnsXML(fields)
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		`xsi:type="varchar" name="title" length="255" nullable="false"`,
		`xsi:type="text" name="body"`,
		`xsi:type="int" name="views" unsigned="false" nullable="true"`,
		`xsi:type="decimal" name="price" scale="4" precision="12" nullable="false"`,
		`xsi:type="boolean" name="active" nullable="false" default="1"`,
		`xsi:type="datetime" name="published_at" nullable="false"`,
		`xsi:type="int" name="author_id" unsigned="true" nullable="false"`,
		`xsi:type="text" name="meta" nullable="false"`,
		`referenceId="SLUG_UNIQUE"`,
		`referenceId="AUTHOR_ID_INDEX"`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("magento columns missing %q in:\n%s", w, out)
		}
	}
}

func TestMagentoColumnsXMLUnsupportedType(t *testing.T) {
	if _, err := MagentoColumnsXML([]Field{{Name: "x", Type: "blob"}}); err == nil {
		t.Fatal("expected unsupported type error")
	}
}

// TestLaravelSchemaLines checks the migration body for each type plus modifiers.
func TestLaravelSchemaLines(t *testing.T) {
	fields := []Field{
		{Name: "title", Type: TypeString},
		{Name: "body", Type: TypeText, Nullable: true},
		{Name: "views", Type: TypeInt, Default: "0"},
		{Name: "price", Type: TypeDecimal},
		{Name: "active", Type: TypeBool, Default: "true"},
		{Name: "published_at", Type: TypeDateTime},
		{Name: "author_id", Type: TypeForeignID},
		{Name: "meta", Type: TypeJSON},
		{Name: "slug", Type: TypeString, Unique: true, Index: true},
	}
	out, err := LaravelSchemaLines(fields)
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		"$table->string('title');",
		"$table->text('body')->nullable();",
		"$table->integer('views')->default(0);",
		"$table->decimal('price', 12, 4);",
		"$table->boolean('active')->default(true);",
		"$table->dateTime('published_at');",
		"$table->foreignId('author_id')->constrained();",
		"$table->json('meta');",
		"$table->string('slug')->unique()->index();",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("laravel schema missing %q in:\n%s", w, out)
		}
	}
}

func TestLaravelSchemaLinesUnsupportedType(t *testing.T) {
	if _, err := LaravelSchemaLines([]Field{{Name: "x", Type: "blob"}}); err == nil {
		t.Fatal("expected unsupported type error")
	}
}

func TestLaravelFillable(t *testing.T) {
	got := LaravelFillable([]Field{{Name: "title"}, {Name: "body"}})
	if got != "'title', 'body'" {
		t.Fatalf("fillable = %q", got)
	}
}

func TestLaravelDefaultLiteralString(t *testing.T) {
	got := laravelDefaultLiteral(Field{Type: TypeString, Default: "he's"})
	if got != `'he\'s'` {
		t.Fatalf("string default literal = %q", got)
	}
	if laravelDefaultLiteral(Field{Type: TypeBool, Default: "0"}) != "false" {
		t.Fatal("bool 0 should be false")
	}
}

// TestRenderMagentoModelWithFields is the end-to-end check that a model rendered
// with typed fields produces a db_schema.xml carrying real columns, not just the
// hardcoded entity_id.
func TestRenderMagentoModelWithFields(t *testing.T) {
	comp, _ := MagentoByKey("model")
	files, err := RenderMagento(comp, MagentoVars{
		Vendor: "Acme", Module: "Blog", Name: "Post",
		Fields: []Field{{Name: "title", Type: TypeString}, {Name: "author_id", Type: TypeForeignID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "db_schema.xml") {
			schema = f.Content
		}
	}
	if !strings.Contains(schema, `name="entity_id"`) {
		t.Fatalf("db_schema lost its primary key:\n%s", schema)
	}
	for _, w := range []string{`name="title"`, `name="author_id"`} {
		if !strings.Contains(schema, w) {
			t.Fatalf("db_schema missing %q:\n%s", w, schema)
		}
	}
}

// TestRenderLaravelModel checks the field-aware Laravel model + migration path.
func TestRenderLaravelModel(t *testing.T) {
	prev := nowStamp
	nowStamp = func() string { return "2026_08_21_120000" }
	defer func() { nowStamp = prev }()

	files, err := RenderLaravelModel(LaravelModelVars{
		Name:   "Order",
		Fields: []Field{{Name: "total", Type: TypeDecimal}, {Name: "note", Type: TypeText, Nullable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Content
	}
	model, ok := got["app/Models/Order.php"]
	if !ok {
		t.Fatalf("model file not produced: %v", got)
	}
	if !strings.Contains(model, "protected $fillable = ['total', 'note'];") {
		t.Fatalf("model $fillable wrong:\n%s", model)
	}
	mig, ok := got["database/migrations/2026_08_21_120000_create_orders_table.php"]
	if !ok {
		t.Fatalf("migration file not produced or misnamed: %v", got)
	}
	for _, w := range []string{"Schema::create('orders'", "$table->decimal('total', 12, 4);", "$table->text('note')->nullable();"} {
		if !strings.Contains(mig, w) {
			t.Fatalf("migration missing %q:\n%s", w, mig)
		}
	}
}

func TestRenderLaravelModelRejectsBadField(t *testing.T) {
	if _, err := RenderLaravelModel(LaravelModelVars{Name: "X", Fields: []Field{{Name: "1bad", Type: TypeString}}}); err == nil {
		t.Fatal("expected a bad field name to be rejected")
	}
}
