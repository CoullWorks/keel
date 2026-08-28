package gen

import (
	"strings"
	"testing"
)

// The typed --field list (the same one the studio's fields table collects) must
// render into a template framework's model, not be dropped. Regression for the
// "gen model produced a fieldless stub" bug.
func TestTemplateModelRendersFields(t *testing.T) {
	fields := []Field{
		{Name: "title", Type: TypeString},
		{Name: "price", Type: TypeDecimal},
		{Name: "in_stock", Type: TypeBool},
	}
	cases := map[string][]string{
		"django":  {"title = models.CharField", "price = models.DecimalField", "in_stock = models.BooleanField"},
		"fastapi": {"title: Mapped[str]", "price: Mapped[float]", "in_stock: Mapped[bool]"},
		"flask":   {"title = db.Column(db.String(255))", "price = db.Column(db.Numeric(10, 2))", "in_stock = db.Column(db.Boolean"},
	}
	for fw, wants := range cases {
		t.Run(fw, func(t *testing.T) {
			files, ok, err := FrameworkRender(fw, "model", "Order", fields, nil)
			if err != nil || !ok || len(files) == 0 {
				t.Fatalf("%s render model: ok=%v err=%v files=%d", fw, ok, err, len(files))
			}
			body := files[0].Content
			for _, w := range wants {
				if !strings.Contains(body, w) {
					t.Errorf("%s model missing %q in:\n%s", fw, w, body)
				}
			}
		})
	}
}
