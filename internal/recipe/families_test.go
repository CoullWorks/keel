package recipe

import "testing"

func TestFamiliesGroupsVariants(t *testing.T) {
	rs := []Recipe{
		{ID: "laravel", Family: ""},
		{ID: "woocommerce", Family: "woocommerce", Variant: "Classic"},
		{ID: "woocommerce-bedrock", Family: "woocommerce", Variant: "Bedrock"},
	}
	fams := Families(rs)
	if len(fams) != 2 {
		t.Fatalf("want 2 families (laravel, woocommerce), got %d", len(fams))
	}
	// laravel: own single-variant group, primary = itself
	if fams[0].Key != "laravel" || len(fams[0].Variants) != 1 || fams[0].Primary.ID != "laravel" {
		t.Fatalf("laravel group wrong: %+v", fams[0])
	}
	// woocommerce: 2 variants, primary = the id==family (classic)
	if fams[1].Key != "woocommerce" || len(fams[1].Variants) != 2 || fams[1].Primary.ID != "woocommerce" {
		t.Fatalf("woocommerce group wrong: %+v", fams[1])
	}
}
