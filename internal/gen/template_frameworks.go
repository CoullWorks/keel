package gen

import (
	"fmt"
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// template_frameworks.go registers the frameworks that ship no rich generator CLI
// and are therefore template-driven, mirroring the Magento strategy: the Node
// meta-frameworks (Next.js, Nuxt, Astro, SvelteKit) and the Python
// micro-frameworks (FastAPI, Flask). Each component renders idiomatic files into
// the framework's conventional paths; none has a module concept, so a UI shows a
// per-file scaffold form, never a "Module" name box.
//
// A framework that has a real generator CLI (Laravel, Symfony, NestJS, Adonis,
// Django's manage.py half) lives in its own file and drives that CLI instead —
// keel only templates where the framework has nothing to drive.

// tmplComponent is one template-driven component: its key, menu label, the file
// it renders (path template + content template) and whether its content is a Go
// text/template needing {{.Name}}/{{.Lower}} substitution. Path is always a
// template so a component can name its own file from the component name.
type tmplComponent struct {
	key, label, path, tmpl string
}

// tmplVars are the substitution inputs shared by every template-driven framework:
// the component name as given, plus its lower-case form for file names and route
// segments. One neutral shape keeps the templates uniform across frameworks.
type tmplVars struct {
	Name  string
	Lower string
	// FieldLines is the pre-rendered typed-column block a model template uses, so
	// `--field name:type` produces real columns instead of a stub. It is the
	// framework's own fieldLines renderer applied to the collected fields; the
	// non-model templates simply do not reference it.
	FieldLines string
}

// registerTemplateFramework registers a template-driven framework from its
// component list, wiring the shared per-key Render. fieldLines renders the typed
// columns for that framework's model template (nil for frameworks with no model,
// like the Node meta-frameworks), so the same --field list the studio collects
// lands in the generated model.
func registerTemplateFramework(family string, comps []tmplComponent, fieldLines func([]Field) string) {
	byKey := make(map[string]tmplComponent, len(comps))
	gens := make([]Generatable, 0, len(comps))
	for _, c := range comps {
		byKey[c.key] = c
		gens = append(gens, componentGeneratable(family, c.key, c.label, recipe.LevelCodeBlock))
	}
	Register(&FrameworkGen{
		Family:     family,
		HasModule:  false,
		Components: gens,
		Render: func(key, name string, fields []Field, _ map[string]any) ([]OutFile, bool, error) {
			c, ok := byKey[key]
			if !ok {
				return nil, false, nil
			}
			if err := ValidateFields(fields); err != nil {
				return nil, true, err
			}
			v := tmplVars{Name: name, Lower: strings.ToLower(name)}
			if fieldLines != nil {
				v.FieldLines = fieldLines(fields)
			}
			path, err := renderTmpl("path", c.path, nil, v)
			if err != nil {
				return nil, true, err
			}
			content, err := renderTmpl("content", c.tmpl, nil, v)
			if err != nil {
				return nil, true, err
			}
			return []OutFile{{Path: path, Content: content}}, true, nil
		},
	})
}

func init() {
	registerTemplateFramework("nextjs", nextComponents, nil)
	registerTemplateFramework("nuxt", nuxtComponents, nil)
	registerTemplateFramework("astro", astroComponents, nil)
	registerTemplateFramework("sveltekit", svelteComponents, nil)
	registerTemplateFramework("fastapi", fastapiComponents, fastapiModelFields)
	registerTemplateFramework("flask", flaskComponents, flaskModelFields)
}

// fastapiModelFields renders typed columns as SQLAlchemy 2.0 mapped columns.
// Empty falls back to nothing extra (the base id column stays).
func fastapiModelFields(fields []Field) string {
	var b strings.Builder
	for _, f := range fields {
		py, col := pyColumnType(f)
		b.WriteString("    " + f.Name + ": Mapped[" + py + "] = mapped_column(" + col + ")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// flaskModelFields renders typed columns as Flask-SQLAlchemy db.Column lines.
func flaskModelFields(fields []Field) string {
	var b strings.Builder
	for _, f := range fields {
		b.WriteString("    " + f.Name + " = db.Column(" + flaskColumnType(f) + ")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// pyColumnType maps a field onto a (Python type, mapped_column args) pair for
// SQLAlchemy 2.0. nullable widens the type to Optional and sets nullable=True.
func pyColumnType(f Field) (string, string) {
	py, args := "str", ""
	switch f.Type {
	case TypeInt, TypeForeignID:
		py = "int"
	case TypeDecimal:
		py = "float"
	case TypeBool:
		py, args = "bool", "default=False"
	case TypeDateTime:
		py = "datetime"
	case TypeJSON:
		py, args = "dict", "default=dict"
	}
	if f.Nullable {
		py += " | None" // 3.10+ union, no typing import needed
		if args == "" {
			args = "nullable=True"
		} else {
			args += ", nullable=True"
		}
	}
	return py, args
}

// flaskColumnType maps a field onto a Flask-SQLAlchemy db.Column(...) body.
func flaskColumnType(f Field) string {
	t := "db.String(255)"
	switch f.Type {
	case TypeInt, TypeForeignID:
		t = "db.Integer"
	case TypeDecimal:
		t = "db.Numeric(10, 2)"
	case TypeBool:
		t = "db.Boolean, default=False"
	case TypeText:
		t = "db.Text"
	case TypeDateTime:
		t = "db.DateTime"
	case TypeJSON:
		t = "db.JSON"
	default:
		if f.Length > 0 {
			t = fmt.Sprintf("db.String(%d)", f.Length)
		}
	}
	if f.Nullable && !strings.Contains(t, "default=") {
		t += ", nullable=True"
	}
	return t
}

// ---- Next.js (App Router, TypeScript, React) — Dan's stack. No "module". ----

var nextComponents = []tmplComponent{
	{"component", "React component", "src/components/{{.Name}}.tsx", tNextComponent},
	{"page", "Page (App Router)", "src/app/{{.Lower}}/page.tsx", tNextPage},
	{"route", "App route (route.ts)", "src/app/{{.Lower}}/route.ts", tNextRoute},
	{"api-route", "API route", "src/app/api/{{.Lower}}/route.ts", tNextRoute},
	{"hook", "Hook (use{{.Name}})", "src/hooks/use{{.Name}}.ts", tNextHook},
	{"layout", "Layout", "src/app/{{.Lower}}/layout.tsx", tNextLayout},
}

const tNextComponent = `export function {{.Name}}() {
  return <div>{{.Name}}</div>;
}
`

const tNextPage = `export default function {{.Name}}Page() {
  return <main>{{.Name}}</main>;
}
`

const tNextRoute = `import { NextResponse } from "next/server";

export async function GET() {
  return NextResponse.json({ ok: true });
}
`

const tNextHook = `import { useState } from "react";

export function use{{.Name}}() {
  const [value, setValue] = useState<unknown>(null);
  return { value, setValue };
}
`

const tNextLayout = `export default function {{.Name}}Layout({
  children,
}: {
  children: React.ReactNode;
}) {
  return <section>{children}</section>;
}
`

// ---- Nuxt 3 (Vue). No "module". ----

var nuxtComponents = []tmplComponent{
	{"component", "Vue component", "components/{{.Name}}.vue", tNuxtComponent},
	{"page", "Page", "pages/{{.Lower}}.vue", tNuxtPage},
	{"composable", "Composable (use{{.Name}})", "composables/use{{.Name}}.ts", tNuxtComposable},
	{"server-route", "Server API route", "server/api/{{.Lower}}.ts", tNuxtServerRoute},
	{"layout", "Layout", "layouts/{{.Lower}}.vue", tNuxtLayout},
}

const tNuxtComponent = `<script setup lang="ts"></script>

<template>
  <div>{{.Name}}</div>
</template>
`

const tNuxtPage = `<script setup lang="ts"></script>

<template>
  <main>{{.Name}}</main>
</template>
`

const tNuxtComposable = `export function use{{.Name}}() {
  const state = useState<unknown>("{{.Lower}}", () => null);
  return { state };
}
`

const tNuxtServerRoute = `export default defineEventHandler(() => {
  return { ok: true };
});
`

const tNuxtLayout = `<template>
  <slot />
</template>
`

// ---- Astro. No "module". ----

var astroComponents = []tmplComponent{
	{"component", "Astro component", "src/components/{{.Name}}.astro", tAstroComponent},
	{"page", "Page", "src/pages/{{.Lower}}.astro", tAstroPage},
	{"layout", "Layout", "src/layouts/{{.Name}}.astro", tAstroLayout},
	{"endpoint", "Endpoint (API route)", "src/pages/{{.Lower}}.ts", tAstroEndpoint},
}

const tAstroComponent = `---
// {{.Name}} component
---

<div>{{.Name}}</div>
`

const tAstroPage = `---
// {{.Name}} page
---

<main>{{.Name}}</main>
`

const tAstroLayout = `---
const { title } = Astro.props;
---

<html>
  <head><title>{title}</title></head>
  <body><slot /></body>
</html>
`

const tAstroEndpoint = `import type { APIRoute } from "astro";

export const GET: APIRoute = () => {
  return new Response(JSON.stringify({ ok: true }), {
    headers: { "Content-Type": "application/json" },
  });
};
`

// ---- SvelteKit (+page / +server file conventions). No "module". ----

var svelteComponents = []tmplComponent{
	{"component", "Svelte component", "src/lib/{{.Name}}.svelte", tSvelteComponent},
	{"page", "+page (route)", "src/routes/{{.Lower}}/+page.svelte", tSveltePage},
	{"server", "+server endpoint", "src/routes/{{.Lower}}/+server.ts", tSvelteServer},
	{"load", "+page.ts load", "src/routes/{{.Lower}}/+page.ts", tSvelteLoad},
	{"store", "Store", "src/lib/stores/{{.Lower}}.ts", tSvelteStore},
}

const tSvelteComponent = `<script lang="ts"></script>

<div>{{.Name}}</div>
`

const tSveltePage = `<script lang="ts">
  export let data;
</script>

<main>{{.Name}}</main>
`

const tSvelteServer = `import { json } from "@sveltejs/kit";
import type { RequestHandler } from "./$types";

export const GET: RequestHandler = () => {
  return json({ ok: true });
};
`

const tSvelteLoad = `import type { PageLoad } from "./$types";

export const load: PageLoad = () => {
  return {};
};
`

const tSvelteStore = `import { writable } from "svelte/store";

export const {{.Lower}} = writable<unknown>(null);
`

// ---- FastAPI (no native CLI — templates). ----

var fastapiComponents = []tmplComponent{
	{"router", "APIRouter", "app/routers/{{.Lower}}.py", tFastAPIRouter},
	{"model", "SQLAlchemy model", "app/models/{{.Lower}}.py", tFastAPIModel},
	{"schema", "Pydantic schema", "app/schemas/{{.Lower}}.py", tFastAPISchema},
	{"dependency", "Dependency", "app/dependencies/{{.Lower}}.py", tFastAPIDependency},
}

const tFastAPIRouter = `from fastapi import APIRouter

router = APIRouter(prefix="/{{.Lower}}", tags=["{{.Lower}}"])


@router.get("")
async def list_{{.Lower}}() -> list[dict]:
    return []
`

const tFastAPIModel = `from datetime import datetime  # noqa: F401 (used when a datetime field is added)

from sqlalchemy.orm import Mapped, mapped_column

from app.database import Base


class {{.Name}}(Base):
    __tablename__ = "{{.Lower}}"

    id: Mapped[int] = mapped_column(primary_key=True)
{{.FieldLines}}
`

const tFastAPISchema = `from pydantic import BaseModel


class {{.Name}}Base(BaseModel):
    pass


class {{.Name}}Create({{.Name}}Base):
    pass


class {{.Name}}Read({{.Name}}Base):
    id: int

    model_config = {"from_attributes": True}
`

const tFastAPIDependency = `from typing import Annotated

from fastapi import Depends


async def get_{{.Lower}}() -> None:
    return None


{{.Name}}Dep = Annotated[None, Depends(get_{{.Lower}})]
`

// ---- Flask (no native make CLI — templates). ----

var flaskComponents = []tmplComponent{
	{"blueprint", "Blueprint", "app/{{.Lower}}/__init__.py", tFlaskBlueprint},
	{"route", "Route (view)", "app/{{.Lower}}/routes.py", tFlaskRoute},
	{"model", "SQLAlchemy model", "app/models/{{.Lower}}.py", tFlaskModel},
	{"form", "WTForms form", "app/forms/{{.Lower}}.py", tFlaskForm},
}

const tFlaskBlueprint = `from flask import Blueprint

{{.Lower}} = Blueprint("{{.Lower}}", __name__)

from . import routes  # noqa: E402,F401
`

const tFlaskRoute = `from flask import jsonify

from . import {{.Lower}}


@{{.Lower}}.route("/{{.Lower}}")
def index():
    return jsonify(ok=True)
`

const tFlaskModel = `from app.extensions import db


class {{.Name}}(db.Model):
    __tablename__ = "{{.Lower}}"

    id = db.Column(db.Integer, primary_key=True)
{{.FieldLines}}
`

const tFlaskForm = `from flask_wtf import FlaskForm
from wtforms import StringField
from wtforms.validators import DataRequired


class {{.Name}}Form(FlaskForm):
    name = StringField("Name", validators=[DataRequired()])
`
