package studio

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coullworks/keel/internal/engine"
)

// --- deriveDSN ---------------------------------------------------------------

func TestDeriveDSNCompose(t *testing.T) {
	cases := []struct {
		name     string
		recipes  []string
		exec     string
		env      map[string]string
		wantEng  dbEngine
		wantDrv  string
		wantHost string
		wantPort string
		wantUser string
		wantName string
	}{
		{
			name:    "compose postgres uses loopback localPort + db/db/db",
			recipes: []string{"laravel", "postgres"}, exec: "docker compose exec -T app",
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "127.0.0.1", wantPort: "55432", wantUser: "db", wantName: "db",
		},
		{
			name:    "compose mysql uses loopback localPort + db/db/db",
			recipes: []string{"laravel", "mysql"}, exec: "docker compose exec -T app",
			wantEng: engineMySQL, wantDrv: "mysql",
			wantHost: "127.0.0.1", wantPort: "33306", wantUser: "db", wantName: "db",
		},
		{
			name:    "native (empty exec) still resolves to the loopback port",
			recipes: []string{"fastapi", "postgres"}, exec: "",
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "127.0.0.1", wantPort: "55432", wantUser: "db", wantName: "db",
		},
		{
			name:    ".env DB_* overrides the derived defaults",
			recipes: []string{"laravel", "mysql"}, exec: "docker compose exec -T app",
			env:     map[string]string{"DB_PORT": "33999", "DB_USERNAME": "shop", "DB_DATABASE": "shopdb", "DB_PASSWORD": "s3cr3t"},
			wantEng: engineMySQL, wantDrv: "mysql",
			wantHost: "127.0.0.1", wantPort: "33999", wantUser: "shop", wantName: "shopdb",
		},
		{
			name:    "container DB_HOST is ignored (studio is on the host)",
			recipes: []string{"laravel", "mysql"}, exec: "docker compose exec -T app",
			env:     map[string]string{"DB_HOST": "db"},
			wantEng: engineMySQL, wantDrv: "mysql",
			wantHost: "127.0.0.1", wantPort: "33306", wantUser: "db", wantName: "db",
		},
		{
			name:    "real DB_HOST is honoured",
			recipes: []string{"laravel", "postgres"}, exec: "docker compose exec -T app",
			env:     map[string]string{"DB_HOST": "10.0.0.5"},
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "10.0.0.5", wantPort: "55432", wantUser: "db", wantName: "db",
		},
		{
			name:    "supabase is treated as postgres",
			recipes: []string{"nextjs", "supabase"}, exec: "docker compose exec -T app",
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "127.0.0.1", wantPort: "55432", wantUser: "db", wantName: "db",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &engine.Manifest{Recipes: c.recipes}
			get := func(k string) string { return c.env[k] }
			d, err := deriveDSN(context.Background(), "/tmp/p", c.exec, m, get)
			if err != nil {
				t.Fatalf("deriveDSN: %v", err)
			}
			if d.Engine != c.wantEng || d.Driver != c.wantDrv {
				t.Fatalf("engine/driver = %s/%s, want %s/%s", d.Engine, d.Driver, c.wantEng, c.wantDrv)
			}
			if d.Host != c.wantHost || d.Port != c.wantPort || d.User != c.wantUser || d.Name != c.wantName {
				t.Fatalf("dsn = %s:%s@%s/%s, want %s:%s@%s/%s",
					d.User, d.Port, d.Host, d.Name, c.wantUser, c.wantPort, c.wantHost, c.wantName)
			}
		})
	}
}

// A Next.js project on hosted Supabase carries no keel db recipe: it points at
// its database entirely through the env. String-matching the recipe ids would
// hand it a local MySQL — the double failure this fixes. These cases prove the
// engine and DSN come from the env/framework, not just the recipe ids.
func TestDeriveDSNSupabaseAndHosted(t *testing.T) {
	cases := []struct {
		name      string
		framework string
		recipes   []string
		env       map[string]string
		wantEng   dbEngine
		wantDrv   string
		wantHost  string
		wantPort  string
		wantUser  string
		wantName  string
	}{
		{
			name: "hosted Supabase via SUPABASE_DB_URL resolves to pgx, not mysql",
			// No db recipe at all — the failing case in the review.
			framework: "nextjs", recipes: []string{"nextjs"},
			env:     map[string]string{"SUPABASE_DB_URL": "postgresql://postgres:pw@db.abc.supabase.co:5432/postgres"},
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "db.abc.supabase.co", wantPort: "5432", wantUser: "postgres", wantName: "postgres",
		},
		{
			name:      "Next + NEXT_PUBLIC_SUPABASE_URL but no DB URL falls back to the local supabase port",
			framework: "nextjs", recipes: []string{"nextjs"},
			env:     map[string]string{"NEXT_PUBLIC_SUPABASE_URL": "https://abc.supabase.co"},
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "127.0.0.1", wantPort: "54322", wantUser: "db", wantName: "db",
		},
		{
			name:      "postgres DATABASE_URL infers pgx even with no db recipe or framework hint",
			framework: "", recipes: []string{"custom"},
			env:     map[string]string{"DATABASE_URL": "postgres://neon:pw@db.example.com:6543/appdb"},
			wantEng: enginePostgres, wantDrv: "pgx",
			wantHost: "db.example.com", wantPort: "6543", wantUser: "neon", wantName: "appdb",
		},
		{
			name:      "a Next project with no Postgres signal at all is still mysql (no false positive)",
			framework: "nextjs-blank", recipes: []string{"nextjs-blank", "mysql"},
			env:     nil,
			wantEng: engineMySQL, wantDrv: "mysql",
			wantHost: "127.0.0.1", wantPort: "33306", wantUser: "db", wantName: "db",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &engine.Manifest{Framework: c.framework, Recipes: c.recipes}
			get := func(k string) string { return c.env[k] }
			d, err := deriveDSN(context.Background(), "/tmp/p", "docker compose exec -T app", m, get)
			if err != nil {
				t.Fatalf("deriveDSN: %v", err)
			}
			if d.Engine != c.wantEng || d.Driver != c.wantDrv {
				t.Fatalf("engine/driver = %s/%s, want %s/%s", d.Engine, d.Driver, c.wantEng, c.wantDrv)
			}
			if d.Host != c.wantHost || d.Port != c.wantPort || d.User != c.wantUser || d.Name != c.wantName {
				t.Fatalf("dsn = %s@%s:%s/%s, want %s@%s:%s/%s",
					d.User, d.Host, d.Port, d.Name, c.wantUser, c.wantHost, c.wantPort, c.wantName)
			}
		})
	}
}

// engineFor must read the env, not only the recipe ids: this is the exact unit
// that decided "local mysql" for a hosted-Supabase Next project before.
func TestEngineForReadsEnvAndFramework(t *testing.T) {
	pgEnv := func(k string) string {
		if k == "SUPABASE_URL" {
			return "https://abc.supabase.co"
		}
		return ""
	}
	if engineFor(&engine.Manifest{Framework: "nextjs", Recipes: []string{"nextjs"}}, pgEnv) != enginePostgres {
		t.Fatal("a SUPABASE_URL env must make the engine postgres, even with no postgres recipe")
	}
	if engineFor(&engine.Manifest{Framework: "nextjs", Recipes: []string{"nextjs"}}, nil) != enginePostgres {
		t.Fatal("a supabase-defaulting framework must be postgres even with no env")
	}
	if engineFor(&engine.Manifest{Framework: "laravel", Recipes: []string{"laravel", "mysql"}}, nil) != engineMySQL {
		t.Fatal("a plain mysql Laravel project must stay mysql")
	}
}

// projectEnvGetter reads .env AND .env.local, with .env.local winning — the
// Node/Next convention. Keying only off .env is why the studio missed a hosted
// project's own credentials.
func TestProjectEnvGetterPrefersEnvLocal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_NAME=base\nDB_USER=base_user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DB_NAME=local\nSUPABASE_DB_URL=postgresql://postgres:pw@db.abc.supabase.co:5432/postgres\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	get := projectEnvGetter(dir)
	if got := get("DB_NAME"); got != "local" {
		t.Fatalf(".env.local should override .env: DB_NAME = %q, want local", got)
	}
	if got := get("DB_USER"); got != "base_user" {
		t.Fatalf("a key only in .env should still be read: DB_USER = %q", got)
	}
	// And the merged env drives the DSN: the .env.local Supabase URL wins.
	m := &engine.Manifest{Framework: "nextjs", Recipes: []string{"nextjs"}}
	d, err := deriveDSN(context.Background(), dir, "docker compose exec -T app", m, get)
	if err != nil {
		t.Fatalf("deriveDSN: %v", err)
	}
	if d.Engine != enginePostgres || d.Host != "db.abc.supabase.co" {
		t.Fatalf("env.local Supabase URL not applied: %+v", d)
	}
}

func TestDeriveDSNDatabaseURLOverride(t *testing.T) {
	m := &engine.Manifest{Recipes: []string{"nextjs", "postgres"}}
	env := map[string]string{"DATABASE_URL": "postgresql://neon:pw@db.example.com:6543/appdb?sslmode=require"}
	d, err := deriveDSN(context.Background(), "/tmp/p", "", m, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("deriveDSN: %v", err)
	}
	if d.Host != "db.example.com" || d.Port != "6543" || d.User != "neon" || d.Password != "pw" || d.Name != "appdb" {
		t.Fatalf("DATABASE_URL not applied: %+v", d)
	}
}

func TestDeriveDSNDdev(t *testing.T) {
	restore := ddevDescribe
	ddevDescribe = func(ctx context.Context, dir string) ([]byte, error) {
		return []byte(`{"raw":{"dbinfo":{"database_type":"postgres","dbPort":"5432","published_port":32771,"host":"db","username":"db","password":"db","dbname":"db"}}}`), nil
	}
	defer func() { ddevDescribe = restore }()

	m := &engine.Manifest{Recipes: []string{"laravel", "postgres"}}
	d, err := deriveDSN(context.Background(), "/tmp/p", "ddev exec", m, nil)
	if err != nil {
		t.Fatalf("deriveDSN(ddev): %v", err)
	}
	if d.Host != "127.0.0.1" || d.Port != "32771" || d.User != "db" {
		t.Fatalf("ddev dsn wrong: %+v", d)
	}
}

func TestDeriveDSNDdevFailsWhenDown(t *testing.T) {
	restore := ddevDescribe
	ddevDescribe = func(ctx context.Context, dir string) ([]byte, error) {
		return nil, fmt.Errorf("ddev is not running")
	}
	defer func() { ddevDescribe = restore }()

	m := &engine.Manifest{Recipes: []string{"laravel", "mysql"}}
	if _, err := deriveDSN(context.Background(), "/tmp/p", "ddev exec", m, nil); err == nil {
		t.Fatalf("a failed ddev describe should error, not silently succeed")
	}
}

func TestDsnFromDdevMissingPort(t *testing.T) {
	if _, err := dsnFromDdev([]byte(`{"raw":{"dbinfo":{"username":"db"}}}`), engineMySQL); err == nil {
		t.Fatalf("no published port should be an error")
	}
}

func TestParseDatabaseURL(t *testing.T) {
	cases := []struct {
		in                           string
		ok                           bool
		host, port, user, pass, name string
	}{
		{"mysql://u:p@h:3306/db", true, "h", "3306", "u", "p", "db"},
		{"postgresql://app@127.0.0.1:5432/appdb?x=1", true, "127.0.0.1", "5432", "app", "", "appdb"},
		{"not a url", false, "", "", "", "", ""},
	}
	for _, c := range cases {
		got, ok := parseDatabaseURL(c.in)
		if ok != c.ok {
			t.Fatalf("%q: ok=%v want %v", c.in, ok, c.ok)
		}
		if !ok {
			continue
		}
		if got.Host != c.host || got.Port != c.port || got.User != c.user || got.Password != c.pass || got.Name != c.name {
			t.Fatalf("%q: parsed %+v", c.in, got)
		}
	}
}

// --- query building ----------------------------------------------------------

func TestBuildSelect(t *testing.T) {
	cols := []string{"id", "name"}
	t.Run("postgres paged, sorted, filtered", func(t *testing.T) {
		q := gridQuery{Table: "users", Page: 2, PageSize: 10, SortCol: "name", SortDesc: true, FilterCol: "name", Filter: "ann"}
		sql, args := buildSelect(enginePostgres, q, cols)
		want := `SELECT "id", "name" FROM "users" WHERE "name"::text LIKE $1 ORDER BY "name" DESC LIMIT 10 OFFSET 10`
		if sql != want {
			t.Fatalf("sql = %q\nwant %q", sql, want)
		}
		if len(args) != 1 || args[0] != "%ann%" {
			t.Fatalf("args = %v, want [%%ann%%]", args)
		}
	})
	t.Run("mysql no filter uses ? and CAST", func(t *testing.T) {
		q := gridQuery{Table: "orders", Page: 1, PageSize: 25, SortCol: "id"}
		sql, args := buildSelect(engineMySQL, q, cols)
		want := "SELECT `id`, `name` FROM `orders` ORDER BY `id` ASC LIMIT 25 OFFSET 0"
		if sql != want {
			t.Fatalf("sql = %q\nwant %q", sql, want)
		}
		if len(args) != 0 {
			t.Fatalf("no filter -> no args, got %v", args)
		}
	})
}

func TestBuildCount(t *testing.T) {
	q := gridQuery{Table: "users", FilterCol: "email", Filter: "@x"}
	sql, args := buildCount(enginePostgres, q)
	want := `SELECT COUNT(*) FROM "users" WHERE "email"::text LIKE $1`
	if sql != want {
		t.Fatalf("count sql = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != "%@x%" {
		t.Fatalf("count args = %v", args)
	}
}

func TestGridQueryNormaliseClamps(t *testing.T) {
	q := gridQuery{Page: -5, PageSize: 100000}
	q.normalise()
	if q.Page != 1 || q.PageSize != maxPageSize {
		t.Fatalf("normalise did not clamp: page=%d size=%d", q.Page, q.PageSize)
	}
	q2 := gridQuery{}
	q2.normalise()
	if q2.PageSize != defaultPageSize {
		t.Fatalf("zero page size should default to %d, got %d", defaultPageSize, q2.PageSize)
	}
}

func TestQuoteIdentDoublesQuotes(t *testing.T) {
	if got := quoteIdent(enginePostgres, `a"b`); got != `"a""b"` {
		t.Fatalf("pg quote = %q", got)
	}
	if got := quoteIdent(engineMySQL, "a`b"); got != "`a``b`" {
		t.Fatalf("mysql quote = %q", got)
	}
}

// --- update building ---------------------------------------------------------

func TestBuildUpdate(t *testing.T) {
	val := "Ada"
	t.Run("postgres single-column pk", func(t *testing.T) {
		sql, args, err := buildUpdate(enginePostgres, "users", "name", &val, map[string]string{"id": "7"}, []string{"id"})
		if err != nil {
			t.Fatal(err)
		}
		want := `UPDATE "users" SET "name" = $1 WHERE "id" = $2`
		if sql != want {
			t.Fatalf("sql = %q, want %q", sql, want)
		}
		if len(args) != 2 || args[0] != "Ada" || args[1] != "7" {
			t.Fatalf("args = %v", args)
		}
	})
	t.Run("mysql composite pk", func(t *testing.T) {
		sql, args, err := buildUpdate(engineMySQL, "line_items", "qty", &val, map[string]string{"order_id": "1", "sku": "A"}, []string{"order_id", "sku"})
		if err != nil {
			t.Fatal(err)
		}
		want := "UPDATE `line_items` SET `qty` = ? WHERE `order_id` = ? AND `sku` = ?"
		if sql != want {
			t.Fatalf("sql = %q, want %q", sql, want)
		}
		if len(args) != 3 {
			t.Fatalf("args = %v", args)
		}
	})
	t.Run("nil value binds SQL NULL", func(t *testing.T) {
		_, args, err := buildUpdate(enginePostgres, "t", "c", nil, map[string]string{"id": "1"}, []string{"id"})
		if err != nil {
			t.Fatal(err)
		}
		if args[0] != nil {
			t.Fatalf("nil value should bind nil, got %v", args[0])
		}
	})
	t.Run("no pk is refused", func(t *testing.T) {
		if _, _, err := buildUpdate(enginePostgres, "t", "c", &val, nil, nil); err == nil {
			t.Fatalf("a table with no pk must not be editable")
		}
	})
	t.Run("edit missing a pk column is refused", func(t *testing.T) {
		if _, _, err := buildUpdate(enginePostgres, "t", "c", &val, map[string]string{"other": "1"}, []string{"id"}); err == nil {
			t.Fatalf("an edit that does not carry every pk column must be refused")
		}
	})
}

func TestValidCol(t *testing.T) {
	cols := []column{{Name: "id"}, {Name: "name"}}
	if !validCol(cols, "name") || validCol(cols, "nope") {
		t.Fatalf("validCol wrong")
	}
}

// --- applyEdits (transaction) via a fake driver ------------------------------

func TestApplyEditsCommitsAllInOneTx(t *testing.T) {
	db, fake := openFakeDB(t)
	defer db.Close()
	one := "1"
	edits := []cellEdit{
		{Column: "name", Value: &one, Key: map[string]string{"id": "1"}},
		{Column: "email", Value: nil, Key: map[string]string{"id": "2"}},
	}
	n, err := applyEdits(context.Background(), db, enginePostgres, "users", edits, []column{{Name: "name"}, {Name: "email"}}, []string{"id"})
	if err != nil {
		t.Fatalf("applyEdits: %v", err)
	}
	if n != 2 {
		t.Fatalf("updated = %d, want 2", n)
	}
	if !fake.committed || fake.rolledBack {
		t.Fatalf("a clean batch should commit, not roll back (commit=%v rollback=%v)", fake.committed, fake.rolledBack)
	}
	if len(fake.execs) != 2 {
		t.Fatalf("expected 2 UPDATEs, got %d: %v", len(fake.execs), fake.execs)
	}
	if !strings.HasPrefix(fake.execs[0], "UPDATE") {
		t.Fatalf("first stmt not an UPDATE: %q", fake.execs[0])
	}
}

func TestApplyEditsRollsBackOnError(t *testing.T) {
	db, fake := openFakeDB(t)
	defer db.Close()
	fake.failOnExec = true
	one := "1"
	edits := []cellEdit{{Column: "name", Value: &one, Key: map[string]string{"id": "1"}}}
	if _, err := applyEdits(context.Background(), db, enginePostgres, "users", edits, []column{{Name: "name"}, {Name: "email"}}, []string{"id"}); err == nil {
		t.Fatalf("a failing exec should error")
	}
	if fake.committed || !fake.rolledBack {
		t.Fatalf("a failed batch must roll back, not commit (commit=%v rollback=%v)", fake.committed, fake.rolledBack)
	}
}

func TestApplyEditsRefusesBadEdit(t *testing.T) {
	db, fake := openFakeDB(t)
	defer db.Close()
	one := "1"
	// This edit is missing the pk column, so buildUpdate refuses it and the tx
	// rolls back before any statement runs.
	edits := []cellEdit{{Column: "name", Value: &one, Key: map[string]string{"nope": "1"}}}
	if _, err := applyEdits(context.Background(), db, enginePostgres, "users", edits, []column{{Name: "name"}, {Name: "email"}}, []string{"id"}); err == nil {
		t.Fatalf("an edit missing its pk column must error")
	}
	if len(fake.execs) != 0 {
		t.Fatalf("no statement should have executed, got %v", fake.execs)
	}
	if !fake.rolledBack {
		t.Fatalf("the tx should roll back")
	}
}

// --- handler wiring: stubbed openDB ------------------------------------------

func TestHandleDBGridConnectErrorSurfaces(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	trackProject(t, dir)
	restore := stubOpenDBErr(t, fmt.Errorf("connection refused"))
	defer restore()
	w := httptest.NewRecorder()
	handleDBGrid(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/grid",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"table":"users"}`)))
	m := decodeJSON(t, w)
	if err, _ := m["error"].(string); !strings.Contains(err, "could not connect") {
		t.Fatalf("grid should surface a connect error: %s", w.Body.String())
	}
}

func TestHandleDBGridBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleDBGrid(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/grid", strings.NewReader(`{"table":""}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); err != "bad request" {
		t.Fatalf("empty table should be a bad request, got %s", w.Body.String())
	}
}

func TestHandleDBCommitBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleDBCommit(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/commit", strings.NewReader(`{"table":"users","edits":[]}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); err != "bad request" {
		t.Fatalf("no edits should be a bad request, got %s", w.Body.String())
	}
}

func TestHandleDBCommitConnectErrorSurfaces(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "mysql"})
	trackProject(t, dir)
	restore := stubOpenDBErr(t, fmt.Errorf("connection refused"))
	defer restore()
	w := httptest.NewRecorder()
	handleDBCommit(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/commit",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"table":"users","edits":[{"column":"name","value":"x","key":{"id":"1"}}]}`)))
	m := decodeJSON(t, w)
	if err, _ := m["error"].(string); !strings.Contains(err, "could not connect") {
		t.Fatalf("commit should surface a connect error: %s", w.Body.String())
	}
}

// --- full handler flows (projectDB seam -> fake DB) --------------------------

func TestHandleDBGridHappyPath(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.script = []scriptedResult{
		{match: "information_schema.tables", cols: []string{"table_name"}, rows: [][]driver.Value{{[]byte("users")}}},
		{match: "information_schema.columns", cols: []string{"column_name", "data_type"}, rows: [][]driver.Value{{[]byte("id"), []byte("integer")}, {[]byte("name"), []byte("text")}}},
		{match: "FOREIGN KEY", cols: []string{"column_name", "referenced_table_name", "referenced_column_name"}, rows: nil}, // no FKs on this table
		{match: "pg_index", cols: []string{"attname"}, rows: [][]driver.Value{{[]byte("id")}}},
		{match: "COUNT(*)", cols: []string{"c"}, rows: [][]driver.Value{{[]byte("2")}}},
		// the paged SELECT (falls through to the default match below)
		{match: `FROM "users"`, cols: []string{"id", "name"}, rows: [][]driver.Value{{[]byte("1"), []byte("Ada")}, {[]byte("2"), nil}}},
	}
	restore := stubProjectDB(t, db, enginePostgres)
	defer restore()

	w := httptest.NewRecorder()
	handleDBGrid(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/grid",
		strings.NewReader(`{"dir":".","table":"users","page":1,"pageSize":50,"sort":"name","desc":true,"filterCol":"name","filter":"a"}`)))
	m := decodeJSON(t, w)
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("happy path should not error: %s", w.Body.String())
	}
	if int(m["total"].(float64)) != 2 {
		t.Fatalf("total = %v", m["total"])
	}
	pk, _ := m["pk"].([]any)
	if len(pk) != 1 || pk[0] != "id" {
		t.Fatalf("pk = %v", m["pk"])
	}
	cols, _ := m["columns"].([]any)
	if len(cols) != 2 {
		t.Fatalf("columns = %v", m["columns"])
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", m["rows"])
	}
}

func TestHandleDBGridUnknownTable(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.script = []scriptedResult{{match: "information_schema.tables", cols: []string{"one"}, rows: nil}} // no rows -> not found
	restore := stubProjectDB(t, db, enginePostgres)
	defer restore()
	w := httptest.NewRecorder()
	handleDBGrid(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/grid",
		strings.NewReader(`{"dir":".","table":"ghost"}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "no such table") {
		t.Fatalf("unknown table should be refused: %s", w.Body.String())
	}
}

func TestHandleDBCommitHappyPath(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.script = []scriptedResult{
		{match: "information_schema.tables", cols: []string{"table_name"}, rows: [][]driver.Value{{[]byte("users")}}},
		{match: "information_schema.columns", cols: []string{"column_name", "data_type"}, rows: [][]driver.Value{{[]byte("id"), []byte("integer")}, {[]byte("name"), []byte("text")}}},
		{match: "PRIMARY", cols: []string{"column_name"}, rows: [][]driver.Value{{[]byte("id")}}},
	}
	restore := stubProjectDB(t, db, engineMySQL)
	defer restore()

	w := httptest.NewRecorder()
	handleDBCommit(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/commit",
		strings.NewReader(`{"dir":".","table":"users","edits":[{"column":"name","value":"Ada","key":{"id":"1"}}]}`)))
	m := decodeJSON(t, w)
	if m["committed"] != true {
		t.Fatalf("commit should succeed: %s", w.Body.String())
	}
	if !st.committed {
		t.Fatalf("the transaction should have committed")
	}
	if len(st.execs) != 1 || !strings.HasPrefix(st.execs[0], "UPDATE") {
		t.Fatalf("one UPDATE should have run: %v", st.execs)
	}
}

func TestHandleDBCommitRejectsUnknownColumn(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.script = []scriptedResult{
		{match: "information_schema.tables", cols: []string{"table_name"}, rows: [][]driver.Value{{[]byte("users")}}},
		{match: "information_schema.columns", cols: []string{"column_name", "data_type"}, rows: [][]driver.Value{{[]byte("id"), []byte("integer")}}},
		{match: "PRIMARY", cols: []string{"column_name"}, rows: [][]driver.Value{{[]byte("id")}}},
	}
	restore := stubProjectDB(t, db, engineMySQL)
	defer restore()
	w := httptest.NewRecorder()
	handleDBCommit(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/commit",
		strings.NewReader(`{"dir":".","table":"users","edits":[{"column":"evil","value":"x","key":{"id":"1"}}]}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "no such column") {
		t.Fatalf("an unknown column must be refused before any write: %s", w.Body.String())
	}
	if len(st.execs) != 0 {
		t.Fatalf("no UPDATE should have run for a bad column: %v", st.execs)
	}
}

func TestHandleDBCommitRefusesTableWithoutPK(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.script = []scriptedResult{
		{match: "information_schema.tables", cols: []string{"table_name"}, rows: [][]driver.Value{{[]byte("users")}}},
		{match: "information_schema.columns", cols: []string{"column_name", "data_type"}, rows: [][]driver.Value{{[]byte("name"), []byte("text")}}},
		{match: "PRIMARY", cols: []string{"column_name"}, rows: nil}, // no pk
	}
	restore := stubProjectDB(t, db, engineMySQL)
	defer restore()
	w := httptest.NewRecorder()
	handleDBCommit(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/commit",
		strings.NewReader(`{"dir":".","table":"logs","edits":[{"column":"name","value":"x","key":{}}]}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "no primary key") {
		t.Fatalf("a pk-less table must refuse edits: %s", w.Body.String())
	}
}

func TestHandleDBTablesHappyPath(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"table_name"}
	st.queryRows = [][]driver.Value{{[]byte("users")}, {[]byte("orders")}}
	restore := stubProjectDB(t, db, enginePostgres)
	defer restore()
	w := httptest.NewRecorder()
	handleDBTables(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/tables", strings.NewReader(`{"dir":"."}`)))
	m := decodeJSON(t, w)
	tables, _ := m["tables"].([]any)
	if len(tables) != 2 || tables[0] != "users" {
		t.Fatalf("tables = %v", m["tables"])
	}
}

// --- pure encoders + introspection (fake-driver backed) ----------------------

func TestDSNString(t *testing.T) {
	pg := dsn{Engine: enginePostgres, Host: "127.0.0.1", Port: "55432", User: "db", Password: "db", Name: "db"}
	if got := pg.string(); !strings.Contains(got, "host=127.0.0.1 port=55432") || !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("pg dsn = %q", got)
	}
	my := dsn{Engine: engineMySQL, Host: "127.0.0.1", Port: "33306", User: "db", Password: "db", Name: "db"}
	if got := my.string(); !strings.HasPrefix(got, "db:db@tcp(127.0.0.1:33306)/db") {
		t.Fatalf("mysql dsn = %q", got)
	}
}

func TestEngineHelpers(t *testing.T) {
	if engineFor(&engine.Manifest{Recipes: []string{"laravel", "postgres"}}, nil) != enginePostgres {
		t.Fatal("postgres recipe should be pg")
	}
	if engineFor(&engine.Manifest{Recipes: []string{"laravel", "mariadb"}}, nil) != engineMySQL {
		t.Fatal("mariadb recipe should be mysql")
	}
	if defaultHostPort(enginePostgres) != "55432" || defaultHostPort(engineMySQL) != "33306" {
		t.Fatal("default host ports wrong")
	}
	if driverFor(enginePostgres) != "pgx" || driverFor(engineMySQL) != "mysql" {
		t.Fatal("driver names wrong")
	}
}

func TestCastTextForLike(t *testing.T) {
	if got := castTextForLike(enginePostgres, "n"); got != `"n"::text` {
		t.Fatalf("pg cast = %q", got)
	}
	if got := castTextForLike(engineMySQL, "n"); got != "CAST(`n` AS CHAR)" {
		t.Fatalf("mysql cast = %q", got)
	}
}

func TestToCell(t *testing.T) {
	if toCell(nil) != nil {
		t.Fatal("nil -> NULL")
	}
	if s := toCell([]byte("hi")); s == nil || *s != "hi" {
		t.Fatalf("[]byte -> string")
	}
	if s := toCell("x"); s == nil || *s != "x" {
		t.Fatalf("string passthrough")
	}
	if s := toCell(int64(42)); s == nil || *s != "42" {
		t.Fatalf("int -> Sprint")
	}
}

func TestColNames(t *testing.T) {
	got := colNames([]column{{Name: "a"}, {Name: "b"}})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("colNames = %v", got)
	}
}

func TestScanRowsEncodesNulls(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"id", "name"}
	st.queryRows = [][]driver.Value{
		{[]byte("1"), []byte("Ada")},
		{[]byte("2"), nil},
	}
	rows, err := db.QueryContext(context.Background(), "SELECT id,name FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out, err := scanRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0][1] == nil || *out[0][1] != "Ada" {
		t.Fatalf("row 0 = %v", out[0])
	}
	if out[1][1] != nil {
		t.Fatalf("a SQL NULL should scan to a nil cell, got %v", out[1][1])
	}
}

func TestListColumns(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"column_name", "data_type"}
	st.queryRows = [][]driver.Value{{[]byte("id"), []byte("integer")}, {[]byte("name"), []byte("text")}}
	cols, err := listColumns(context.Background(), db, enginePostgres, "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || cols[0].Name != "id" || cols[0].Type != "integer" {
		t.Fatalf("cols = %+v", cols)
	}
}

func TestPrimaryKey(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"column_name"}
	st.queryRows = [][]driver.Value{{[]byte("id")}}
	pk, err := primaryKey(context.Background(), db, engineMySQL, "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(pk) != 1 || pk[0] != "id" {
		t.Fatalf("pk = %v", pk)
	}
}

func TestForeignKeys(t *testing.T) {
	t.Run("single-column FKs are detected", func(t *testing.T) {
		db, st := openFakeDB(t)
		defer db.Close()
		st.queryCols = []string{"column_name", "referenced_table_name", "referenced_column_name"}
		st.queryRows = [][]driver.Value{
			{[]byte("user_id"), []byte("users"), []byte("id")},
			{[]byte("product_id"), []byte("products"), []byte("sku")},
		}
		fks, err := foreignKeys(context.Background(), db, engineMySQL, "orders")
		if err != nil {
			t.Fatal(err)
		}
		if len(fks) != 2 {
			t.Fatalf("want 2 FKs, got %d: %+v", len(fks), fks)
		}
		if fks["user_id"].Table != "users" || fks["user_id"].Column != "id" {
			t.Fatalf("user_id FK = %+v", fks["user_id"])
		}
		if fks["product_id"].Table != "products" || fks["product_id"].Column != "sku" {
			t.Fatalf("product_id FK = %+v", fks["product_id"])
		}
	})
	t.Run("composite FKs are dropped (no single value to link on)", func(t *testing.T) {
		db, st := openFakeDB(t)
		defer db.Close()
		// A two-column FK reports the same local column set twice; neither column
		// can be followed by a single value, so both are dropped.
		st.queryCols = []string{"column_name", "referenced_table_name", "referenced_column_name"}
		st.queryRows = [][]driver.Value{
			{[]byte("order_id"), []byte("orders"), []byte("id")},
			{[]byte("order_id"), []byte("orders"), []byte("id")},
			{[]byte("single"), []byte("t"), []byte("id")},
		}
		fks, err := foreignKeys(context.Background(), db, enginePostgres, "line_items")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := fks["order_id"]; ok {
			t.Fatalf("composite FK column should be dropped: %+v", fks)
		}
		if fks["single"].Table != "t" {
			t.Fatalf("single-column FK should remain: %+v", fks)
		}
	})
}

func TestAttachFKs(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"column_name", "referenced_table_name", "referenced_column_name"}
	st.queryRows = [][]driver.Value{{[]byte("user_id"), []byte("users"), []byte("id")}}
	cols := []column{{Name: "id"}, {Name: "user_id"}, {Name: "note"}}
	got := attachFKs(context.Background(), db, enginePostgres, "orders", cols)
	if got[0].FK != nil || got[2].FK != nil {
		t.Fatalf("non-FK columns must stay unmarked: %+v", got)
	}
	if got[1].FK == nil || got[1].FK.Table != "users" || got[1].FK.Column != "id" {
		t.Fatalf("user_id should carry its FK target: %+v", got[1].FK)
	}
}

func TestTableExists(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"one"}
	st.queryRows = [][]driver.Value{{[]byte("1")}}
	ok, err := tableExists(context.Background(), db, enginePostgres, "users")
	if err != nil || !ok {
		t.Fatalf("existing table should be ok: ok=%v err=%v", ok, err)
	}
	// No rows -> not found.
	st.queryRows = nil
	ok, err = tableExists(context.Background(), db, enginePostgres, "ghost")
	if err != nil || ok {
		t.Fatalf("missing table should be not-ok: ok=%v err=%v", ok, err)
	}
}

func TestListTables(t *testing.T) {
	db, st := openFakeDB(t)
	defer db.Close()
	st.queryCols = []string{"table_name"}
	st.queryRows = [][]driver.Value{{[]byte("users")}, {[]byte("orders")}}
	got, err := listTables(context.Background(), db, engineMySQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "users" || got[1] != "orders" {
		t.Fatalf("tables = %v", got)
	}
}

// --- test helpers ------------------------------------------------------------

// stubOpenDBErr swaps the openDB seam for one that always fails to connect, so a
// handler's degrade-gracefully path can be exercised without a database.
func stubOpenDBErr(t *testing.T, err error) func() {
	t.Helper()
	prev := openDB
	openDB = func(ctx context.Context, d dsn) (*sql.DB, error) { return nil, err }
	return func() { openDB = prev }
}

// stubProjectDB swaps the projectDB seam so a handler runs its full flow against
// a fake *sql.DB — no manifest, no .env, no network.
func stubProjectDB(t *testing.T, db *sql.DB, eng dbEngine) func() {
	t.Helper()
	prev := projectDB
	projectDB = func(ctx context.Context, dir string) (*sql.DB, dbEngine, error) { return db, eng, nil }
	return func() { projectDB = prev }
}

// TestStructuredDBHandlersBoundTheContext proves the grid/tables/commit handlers
// hand projectDB a context that carries a deadline, so a wedged database cannot
// hang the request forever (it degrades to a clear error instead of a spinner
// that never resolves). handleDBQuery is deliberately excluded — a user's raw SQL
// can be a legitimate long-running statement and is cancelled by the stream Stop.
func TestStructuredDBHandlersBoundTheContext(t *testing.T) {
	var gotDeadline bool
	prev := projectDB
	projectDB = func(ctx context.Context, dir string) (*sql.DB, dbEngine, error) {
		_, gotDeadline = ctx.Deadline()
		// Fail fast: the point of the test is the context, not the query path.
		return nil, "", fmt.Errorf("stub: not connecting")
	}
	defer func() { projectDB = prev }()

	cases := []struct {
		name string
		fn   func(w http.ResponseWriter, r *http.Request)
		body string
		path string
	}{
		{"grid", handleDBGrid, `{"dir":".","table":"users"}`, "/api/db/grid"},
		{"tables", handleDBTables, `{"dir":"."}`, "/api/db/tables"},
		{"commit", handleDBCommit, `{"dir":".","table":"users","edits":[{"column":"name","value":"x","key":{"id":"1"}}]}`, "/api/db/commit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDeadline = false
			w := httptest.NewRecorder()
			tc.fn(w, httptest.NewRequest("POST", "http://127.0.0.1"+tc.path, strings.NewReader(tc.body)))
			if !gotDeadline {
				t.Errorf("%s handler passed projectDB a context with no deadline — a wedged DB would hang", tc.name)
			}
		})
	}
}

// --- a minimal in-memory database/sql driver for transaction tests -----------
//
// It records the statements ExecContext receives and whether the tx committed or
// rolled back, so applyEdits can be verified with zero network and zero real DB.

type fakeConnState struct {
	mu         sync.Mutex
	execs      []string
	committed  bool
	rolledBack bool
	failOnExec bool
	// queryResult is the canned result the next QueryContext returns, so the
	// driver-backed helpers (scanRows, listColumns, …) can run without a real DB.
	queryCols []string
	queryRows [][]driver.Value
	// script routes a query to a canned result by substring match (first hit
	// wins), so a handler that runs several queries in sequence — exists,
	// columns, pk, count, select — can be exercised end to end.
	script []scriptedResult
}

type scriptedResult struct {
	match string
	cols  []string
	rows  [][]driver.Value
}

var fakeRegistry = struct {
	mu sync.Mutex
	n  int
	st map[string]*fakeConnState
}{st: map[string]*fakeConnState{}}

func init() {
	sql.Register("keelfake", fakeDriver{})
}

// openFakeDB opens a fresh fake DB with its own recording state.
func openFakeDB(t *testing.T) (*sql.DB, *fakeConnState) {
	t.Helper()
	fakeRegistry.mu.Lock()
	fakeRegistry.n++
	name := fmt.Sprintf("db%d", fakeRegistry.n)
	st := &fakeConnState{}
	fakeRegistry.st[name] = st
	fakeRegistry.mu.Unlock()
	db, err := sql.Open("keelfake", name)
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	return db, st
}

type fakeDriver struct{}

func (fakeDriver) Open(name string) (driver.Conn, error) {
	fakeRegistry.mu.Lock()
	st := fakeRegistry.st[name]
	fakeRegistry.mu.Unlock()
	if st == nil {
		st = &fakeConnState{}
	}
	return &fakeConn{st: st}, nil
}

type fakeConn struct{ st *fakeConnState }

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeConn) Close() error                              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)                 { return &fakeTx{st: c.st}, nil }

// ExecerContext lets database/sql call Exec without a prepared statement.
func (c *fakeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	if c.st.failOnExec {
		return nil, fmt.Errorf("exec failed")
	}
	c.st.execs = append(c.st.execs, query)
	return fakeResult{}, nil
}

// QueryerContext returns the canned rows scripted on the state, so the
// introspection/scan helpers can be exercised with no network. A script (by
// query-substring) takes precedence, so a multi-query handler flow can be
// driven; otherwise the single queryCols/queryRows is returned.
func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	for _, s := range c.st.script {
		if strings.Contains(query, s.match) {
			return &fakeRows{cols: s.cols, rows: s.rows}, nil
		}
	}
	return &fakeRows{cols: c.st.queryCols, rows: c.st.queryRows}, nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

type fakeTx struct{ st *fakeConnState }

func (t *fakeTx) Commit() error {
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	t.st.committed = true
	return nil
}
func (t *fakeTx) Rollback() error {
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	t.st.rolledBack = true
	return nil
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

var (
	_ driver.ExecerContext  = (*fakeConn)(nil)
	_ driver.QueryerContext = (*fakeConn)(nil)
)
