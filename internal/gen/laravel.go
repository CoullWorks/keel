package gen

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/coullworks/keel/internal/recipe"
)

// init registers Laravel's generation stance: it is CLI-driven (keel drives
// `artisan make:*`) and has no module concept (its generatables are per-file, so
// a UI must not show a "Module" name box for a Laravel project). The Command
// builder reuses the Laravel-specific Command via a component lookup, so the
// registry path and the direct gen.Command path produce identical argv.
func init() {
	comps := make([]Generatable, 0, len(LaravelComponents)+1)
	for _, c := range LaravelComponents {
		comps = append(comps, componentGeneratable("laravel", c.Key, c.Label, recipe.LevelCodeBlock))
	}
	// A Laravel project can generate either into its own app tree (the artisan
	// code-blocks above) or as a distributable Composer package (Spatie-style
	// package skeleton). The package generatable carries the Target input the
	// fitness review asks for; it renders files rather than driving artisan,
	// because artisan writes into the app tree and cannot scaffold a package.
	comps = append(comps, componentGeneratable("laravel", "package", "Composer package (Spatie skeleton)", recipe.LevelPackage))
	Register(&FrameworkGen{
		Family:     "laravel",
		HasModule:  false,
		Components: comps,
		Command: func(env, key, name string) ([]string, bool) {
			// The package key renders files (Render below); every other Laravel key
			// drives artisan.
			if key == "package" {
				return nil, false
			}
			c, ok := ByKey(key)
			if !ok {
				return nil, false
			}
			return Command(env, c, name), true
		},
		Render: func(key, name string, _ []Field, params map[string]any) ([]OutFile, bool, error) {
			if key != "package" {
				return nil, false, nil
			}
			target := TargetPackage
			if s, ok := params["target"].(string); ok && Target(s) == TargetAppCode {
				target = TargetAppCode
			}
			vendor, _ := params["vendor"].(string)
			files, err := RenderLaravelPackage(LaravelPackageVars{Vendor: vendor, Name: name, Target: target})
			return files, true, err
		},
	})
}

// LaravelPackageVars are the inputs for a Spatie-style Laravel package: the
// package name (StudlyCase) and its vendor, plus the Target that decides where
// the files land (a Composer package under packages/<vendor>/<name> for
// TargetPackage, or straight into the app tree for TargetAppCode).
type LaravelPackageVars struct {
	Vendor string
	Name   string
	Target Target
}

// Slug is the lower-case config key/file basename for the package (a Laravel
// package config lives at config/<slug>.php and merges under the <slug> key).
func (v LaravelPackageVars) Slug() string { return strings.ToLower(v.Name) }

// RenderLaravelPackage renders a minimal but real Spatie-flavoured Laravel
// package: composer.json (PSR-4 autoload, package discovery), a service provider
// and a config file. It is the package half of the app-vs-package Target the
// fitness review wants: `keel gen package Blog` scaffolds a distributable package
// instead of adding to the app. A missing vendor defaults to "vendor" so the
// PSR-4 namespace is always valid.
func RenderLaravelPackage(v LaravelPackageVars) ([]OutFile, error) {
	if err := ValidateName(v.Name); err != nil {
		return nil, err
	}
	if v.Vendor == "" {
		v.Vendor = "Vendor"
	} else if err := ValidateName(v.Vendor); err != nil {
		return nil, fmt.Errorf("package vendor: %w", err)
	}
	base := "packages/" + strings.ToLower(v.Vendor) + "/" + strings.ToLower(v.Name)
	if v.Target == TargetAppCode {
		base = filepath.Join("app", "Packages", v.Name)
	}
	composer, err := renderTmpl("composer", tLaravelPkgComposer, nil, v)
	if err != nil {
		return nil, err
	}
	provider, err := renderTmpl("provider", tLaravelPkgProvider, nil, v)
	if err != nil {
		return nil, err
	}
	config, err := renderTmpl("config", tLaravelPkgConfig, nil, v)
	if err != nil {
		return nil, err
	}
	return []OutFile{
		{Path: filepath.Join(base, "composer.json"), Content: composer},
		{Path: filepath.Join(base, "src", v.Name+"ServiceProvider.php"), Content: provider},
		{Path: filepath.Join(base, "config", strings.ToLower(v.Name)+".php"), Content: config},
	}, nil
}

// LaravelModelVars are the inputs for a field-aware Laravel model. Fields drives
// both the migration body and the model's $fillable, so a Laravel model produced
// with fields ships real columns instead of the empty migration artisan make
// leaves behind.
type LaravelModelVars struct {
	Name   string
	Fields []Field
}

// laravelFuncs exposes the field renderers to the templates. schemaLines and
// fillable already return strings (the error-returning entry points are called
// ahead of Execute so a bad field fails before any file is written), so the
// template just splats them in.
var laravelFuncs = template.FuncMap{
	"schemaLines": func(fs []Field) (string, error) { return LaravelSchemaLines(fs) },
	"fillable":    LaravelFillable,
	"lowerPlural": tableName,
}

// tableName is the migration/table name for a model: snake_case, pluralised the
// naive Laravel way (append "s"). It matches the common case without pulling in
// an inflector; a recipe author who needs an irregular plural renames the file.
func tableName(name string) string {
	return snake(name) + "s"
}

// snake converts a StudlyCase model name to snake_case.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nowStamp is the migration timestamp source, a package var so a test can pin it
// and assert a deterministic filename.
var nowStamp = func() string { return time.Now().Format("2006_01_02_150405") }

// RenderLaravelModel renders the model class and its migration as OutFiles.
// Unlike the artisan path (which shells out and leaves the migration empty), this
// writes the typed columns straight in, so `keel gen model Order --field ...`
// yields a migration that actually creates the table.
func RenderLaravelModel(v LaravelModelVars) ([]OutFile, error) {
	if err := ValidateFields(v.Fields); err != nil {
		return nil, err
	}
	model, err := renderTmpl("model", tLaravelModel, laravelFuncs, v)
	if err != nil {
		return nil, err
	}
	migration, err := renderTmpl("migration", tLaravelMigration, laravelFuncs, v)
	if err != nil {
		return nil, err
	}
	stamp := nowStamp()
	return []OutFile{
		{Path: fmt.Sprintf("app/Models/%s.php", v.Name), Content: model},
		{Path: fmt.Sprintf("database/migrations/%s_create_%s_table.php", stamp, tableName(v.Name)), Content: migration},
	}, nil
}

// renderTmpl parses and executes one named template with the given funcs.
func renderTmpl(name, tmpl string, funcs template.FuncMap, v any) (string, error) {
	t, err := template.New(name).Funcs(funcs).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, v); err != nil {
		return "", err
	}
	return sb.String(), nil
}

const tLaravelModel = `<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class {{.Name}} extends Model
{
    protected $fillable = [{{fillable .Fields}}];
}
`

const tLaravelMigration = `<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('{{lowerPlural .Name}}', function (Blueprint $table) {
            $table->id();
{{schemaLines .Fields}}            $table->timestamps();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('{{lowerPlural .Name}}');
    }
};
`

const tLaravelPkgComposer = `{
    "name": "{{.Vendor}}/{{.Name}}",
    "description": "The {{.Name}} package",
    "type": "library",
    "license": "MIT",
    "require": {
        "php": "^8.2",
        "illuminate/support": "^11.0|^12.0"
    },
    "autoload": {
        "psr-4": {
            "{{.Vendor}}\\{{.Name}}\\": "src/"
        }
    },
    "extra": {
        "laravel": {
            "providers": [
                "{{.Vendor}}\\{{.Name}}\\{{.Name}}ServiceProvider"
            ]
        }
    }
}
`

const tLaravelPkgProvider = `<?php

namespace {{.Vendor}}\{{.Name}};

use Illuminate\Support\ServiceProvider;

class {{.Name}}ServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../config/{{.Slug}}.php', '{{.Slug}}');
    }

    public function boot(): void
    {
        $this->publishes([
            __DIR__ . '/../config/{{.Slug}}.php' => config_path('{{.Slug}}.php'),
        ], '{{.Slug}}-config');
    }
}
`

const tLaravelPkgConfig = `<?php

return [
    // {{.Name}} package configuration.
];
`
