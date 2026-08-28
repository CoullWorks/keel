// Package features holds Keel's BDD suite: the routes in docs/ROUTES.md
// expressed as executable Gherkin scenarios, run through godog under `go test`.
package features

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/cucumber/godog"
)

type world struct {
	reg  *recipe.Registry
	plan *resolver.Plan
	err  error
}

func (w *world) theBuiltinCatalogue() error {
	reg, err := catalog.Registry()
	w.reg = reg
	return err
}

func (w *world) iSelect(list string) error {
	var ids []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			ids = append(ids, s)
		}
	}
	w.plan, w.err = resolver.Resolve(w.reg, ids)
	return nil
}

func (w *world) planIsValid() error {
	if w.err != nil {
		return fmt.Errorf("expected a valid plan, got error: %v", w.err)
	}
	return nil
}

func (w *world) frameworkIs(fw string) error {
	if w.plan == nil {
		return fmt.Errorf("no plan")
	}
	if w.plan.Framework != fw {
		return fmt.Errorf("framework = %q, want %q", w.plan.Framework, fw)
	}
	return nil
}

func (w *world) firstRecipeIs(id string) error {
	if w.plan == nil || len(w.plan.Recipes) == 0 {
		return fmt.Errorf("empty plan")
	}
	if w.plan.Recipes[0].ID != id {
		return fmt.Errorf("first recipe = %q, want %q", w.plan.Recipes[0].ID, id)
	}
	return nil
}

func (w *world) planRejectedBecause(substr string) error {
	if w.err == nil {
		return fmt.Errorf("expected rejection containing %q, but the plan was valid", substr)
	}
	if !strings.Contains(w.err.Error(), substr) {
		return fmt.Errorf("error %q does not contain %q", w.err.Error(), substr)
	}
	return nil
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = world{}
		return ctx, nil
	})
	sc.Step(`^the built-in recipe catalogue$`, w.theBuiltinCatalogue)
	sc.Step(`^I select "([^"]*)"$`, w.iSelect)
	sc.Step(`^the plan is valid$`, w.planIsValid)
	sc.Step(`^the framework is "([^"]*)"$`, w.frameworkIs)
	sc.Step(`^the first recipe is "([^"]*)"$`, w.firstRecipeIs)
	sc.Step(`^the plan is rejected because "([^"]*)"$`, w.planRejectedBecause)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format: "pretty",
			// Scope to this suite's own feature — "." would recurse into ./e2e and
			// pull in the e2e scenarios (whose steps aren't registered here).
			Paths:    []string{"routes.feature"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("BDD scenarios failed")
	}
}
