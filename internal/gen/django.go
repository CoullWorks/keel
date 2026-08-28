package gen

import (
	"fmt"
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// django.go generates for Django. Django is a hybrid: two of its generators are
// real manage.py commands keel drives (startapp scaffolds an app, makemigrations
// diffs the models), and the rest are template-based because Django has no
// `manage.py make:model` — it is the Python analogue of the Laravel integration
// keel already has, so keel drives what Django ships and templates the rest.
//
// The FrameworkGen therefore sets BOTH Command (for the two manage.py keys) and
// Render (for the templated keys); each returns ok only for the keys it owns, so
// a caller tries Command first and falls through to Render. Django has no module
// concept in the Magento/Nest sense (startapp makes a Python package, not a
// grouped @Module), so HasModule stays false.

// djangoCLI is the set of Django keys that are real manage.py commands rather
// than templates: startapp scaffolds an app, makemigrations diffs the schema.
var djangoCLI = map[string]string{
	"startapp":       "startapp",
	"makemigrations": "makemigrations",
}

// djangoTemplateComponent is a template-driven Django artifact: a component key,
// its menu label, and the single file it renders (path template, content
// template). Django templates are single-file — a model goes in models.py, an
// admin registration in admin.py — so one file per component is the right grain.
type djangoTemplateComponent struct {
	key, label, path, tmpl string
}

// djangoTemplates is the template-driven catalogue: model, view (CBV), DRF
// serializer + viewset, admin registration, form and a management command. Each
// renders idiomatic Django/DRF into the conventional module path, using {{.Name}}
// for the class and {{.Lower}} for the file/attribute forms.
var djangoTemplates = []djangoTemplateComponent{
	{"model", "Model", "models.py", tDjangoModel},
	{"view", "View (class-based)", "views.py", tDjangoView},
	{"serializer", "DRF serializer", "serializers.py", tDRFSerializer},
	{"viewset", "DRF viewset", "views.py", tDRFViewSet},
	{"admin", "Admin registration", "admin.py", tDjangoAdmin},
	{"form", "Form", "forms.py", tDjangoForm},
	{"management-command", "Management command", "management/commands/{{.Lower}}.py", tDjangoCommand},
}

// djangoTemplateByKey returns a Django template component by key.
func djangoTemplateByKey(key string) (djangoTemplateComponent, bool) {
	for _, c := range djangoTemplates {
		if c.key == key {
			return c, true
		}
	}
	return djangoTemplateComponent{}, false
}

// DjangoKeys lists every Django component key (CLI + template), in menu order,
// for help text and completion.
func DjangoKeys() []string {
	out := []string{"startapp", "makemigrations"}
	for _, c := range djangoTemplates {
		out = append(out, c.key)
	}
	return out
}

// djangoVars are the template inputs for a Django component: the class name and
// its lower-case form for file names and DRF url_path/attribute niceties.
type djangoVars struct {
	Name       string
	Lower      string
	FieldLines string // pre-rendered model columns (models.py)
	StrExpr    string // the __str__ return expression
}

// djangoCommand drives the two manage.py generators. `<env> exec python
// manage.py startapp <name>` scaffolds an app; makemigrations takes no name (it
// diffs every app), so keel omits it. Returns ok=false for a template key, which
// FrameworkRender handles instead.
func djangoCommand(env, key, name string) ([]string, bool) {
	sub, ok := djangoCLI[key]
	if !ok {
		return nil, false
	}
	envFields := strings.Fields(env)
	argv := make([]string, 0, len(envFields)+5)
	argv = append(argv, envFields...)
	argv = append(argv, "exec", "python", "manage.py", sub)
	if key == "startapp" && name != "" {
		argv = append(argv, name)
	}
	return argv, true
}

// djangoRender renders a template-driven Django component to its file. Returns
// ok=false for a CLI key (startapp/makemigrations), which djangoCommand handles.
//
// A model carries the typed --field list (the same list the studio's fields table
// collects), so `keel gen model Order --field title:string --field price:decimal`
// writes real Django columns, not a fieldless stub. This is what makes the CLI and
// the studio agree: both thread the same fields into the same render.
func djangoRender(key, name string, fields []Field, _ map[string]any) ([]OutFile, bool, error) {
	c, ok := djangoTemplateByKey(key)
	if !ok {
		return nil, false, nil
	}
	if err := ValidateFields(fields); err != nil {
		return nil, true, err
	}
	v := djangoVars{Name: name, Lower: strings.ToLower(name), FieldLines: djangoModelFields(fields), StrExpr: djangoStrExpr(fields)}
	path, err := renderTmpl("djangoPath", c.path, nil, v)
	if err != nil {
		return nil, true, err
	}
	content, err := renderTmpl("django", c.tmpl, nil, v)
	if err != nil {
		return nil, true, err
	}
	return []OutFile{{Path: path, Content: content}}, true, nil
}

// djangoModelFields renders the typed fields as Django model columns. An empty
// list falls back to a single `name` char field so a model is still usable.
func djangoModelFields(fields []Field) string {
	if len(fields) == 0 {
		return "    name = models.CharField(max_length=255)"
	}
	var b strings.Builder
	for _, f := range fields {
		b.WriteString("    " + f.Name + " = models." + djangoFieldExpr(f) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// djangoStrExpr picks the __str__ return: the first string/text field if there is
// one, else the primary key, so the admin/repr is sensible whatever the columns.
func djangoStrExpr(fields []Field) string {
	for _, f := range fields {
		if f.Type == TypeString || f.Type == TypeText {
			return "self." + f.Name
		}
	}
	if len(fields) == 0 {
		return "self.name"
	}
	return "str(self.pk)"
}

// djangoFieldExpr maps one typed field onto its Django model field call. nullable
// adds null/blank; a string length overrides the default max_length.
func djangoFieldExpr(f Field) string {
	nul := ""
	if f.Nullable {
		nul = "null=True, blank=True"
	}
	join := func(parts ...string) string {
		out := parts[:0]
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, ", ")
	}
	switch f.Type {
	case TypeInt:
		return "IntegerField(" + join(nul) + ")"
	case TypeDecimal:
		return "DecimalField(" + join("max_digits=10", "decimal_places=2", nul) + ")"
	case TypeBool:
		return "BooleanField(default=False)"
	case TypeText:
		return "TextField(" + join(nul) + ")"
	case TypeDateTime:
		return "DateTimeField(" + join(nul) + ")"
	case TypeForeignID:
		return "ForeignKey(\"self\", on_delete=models.CASCADE" + comma(nul) + ")" // edit the target model
	case TypeJSON:
		return "JSONField(" + join("default=dict", nul) + ")"
	default: // string
		max := "max_length=255"
		if f.Length > 0 {
			max = "max_length=" + itoa(f.Length)
		}
		return "CharField(" + join(max, nul) + ")"
	}
}

func comma(s string) string {
	if s == "" {
		return ""
	}
	return ", " + s
}
func itoa(n int) string { return fmt.Sprintf("%d", n) }

func init() {
	comps := make([]Generatable, 0, len(djangoTemplates)+2)
	comps = append(comps,
		componentGeneratable("django", "startapp", "App (manage.py startapp)", recipe.LevelCodeBlock),
		componentGeneratable("django", "makemigrations", "Migrations (makemigrations)", recipe.LevelCodeBlock),
	)
	for _, c := range djangoTemplates {
		comps = append(comps, componentGeneratable("django", c.key, c.label, recipe.LevelCodeBlock))
	}
	Register(&FrameworkGen{
		Family:     "django",
		HasModule:  false,
		Components: comps,
		Command:    djangoCommand,
		Render:     djangoRender,
	})
}

// renderTmpl is shared with laravel.go (same file, package gen) — declared there.
// The Django templates below are plain text/template strings; a nil FuncMap is
// fine because they use only field interpolation.

const tDjangoModel = `from django.db import models


class {{.Name}}(models.Model):
{{.FieldLines}}
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self) -> str:
        return {{.StrExpr}}
`

const tDjangoView = `from django.views.generic import ListView

from .models import {{.Name}}


class {{.Name}}ListView(ListView):
    model = {{.Name}}
    context_object_name = "{{.Lower}}_list"
`

const tDRFSerializer = `from rest_framework import serializers

from .models import {{.Name}}


class {{.Name}}Serializer(serializers.ModelSerializer):
    class Meta:
        model = {{.Name}}
        fields = "__all__"
`

const tDRFViewSet = `from rest_framework import viewsets

from .models import {{.Name}}
from .serializers import {{.Name}}Serializer


class {{.Name}}ViewSet(viewsets.ModelViewSet):
    queryset = {{.Name}}.objects.all()
    serializer_class = {{.Name}}Serializer
`

const tDjangoAdmin = `from django.contrib import admin

from .models import {{.Name}}


@admin.register({{.Name}})
class {{.Name}}Admin(admin.ModelAdmin):
    list_display = ("id",)
`

const tDjangoForm = `from django import forms

from .models import {{.Name}}


class {{.Name}}Form(forms.ModelForm):
    class Meta:
        model = {{.Name}}
        fields = "__all__"
`

const tDjangoCommand = `from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "{{.Name}} command"

    def handle(self, *args, **options) -> None:
        self.stdout.write(self.style.SUCCESS("{{.Name}} ran"))
`
