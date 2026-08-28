package gen

import (
	"strings"
	"testing"
)

func TestRenderMagentoModel(t *testing.T) {
	comp, ok := MagentoByKey("model")
	if !ok {
		t.Fatal("model component missing")
	}
	files, err := RenderMagento(comp, MagentoVars{Vendor: "Acme", Module: "Blog", Name: "Post"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Content
	}
	want := map[string]string{
		"app/code/Acme/Blog/Model/Post.php":                          "class Post extends AbstractModel",
		"app/code/Acme/Blog/Model/ResourceModel/Post.php":            "acme_blog_post",
		"app/code/Acme/Blog/Model/ResourceModel/Post/Collection.php": "class Collection extends AbstractCollection",
		"app/code/Acme/Blog/etc/db_schema.xml":                       `name="acme_blog_post"`,
	}
	for path, needle := range want {
		c, ok := got[path]
		if !ok {
			t.Fatalf("expected file %s not rendered", path)
		}
		if !strings.Contains(c, needle) {
			t.Fatalf("%s missing %q:\n%s", path, needle, c)
		}
	}
	if !strings.Contains(got["app/code/Acme/Blog/Model/Post.php"], `namespace Acme\Blog\Model;`) {
		t.Fatalf("wrong namespace:\n%s", got["app/code/Acme/Blog/Model/Post.php"])
	}
}

func TestRenderModuleBootstrap(t *testing.T) {
	comp, _ := MagentoByKey("module")
	files, err := RenderMagento(comp, MagentoVars{Vendor: "Acme", Module: "Blog"})
	if err != nil {
		t.Fatal(err)
	}
	var reg, composer string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "registration.php") {
			reg = f.Content
		}
		if strings.HasSuffix(f.Path, "composer.json") {
			composer = f.Content
		}
	}
	if !strings.Contains(reg, "'Acme_Blog'") {
		t.Fatalf("registration wrong:\n%s", reg)
	}
	if !strings.Contains(composer, `"acme/module-blog"`) {
		t.Fatalf("composer name wrong:\n%s", composer)
	}
}
