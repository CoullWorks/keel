// This file turns the studio's DB panel from a shell-out SQL prompt into a real
// data grid. It connects to the running project's database with a real Go SQL
// driver (pgx for Postgres, go-sql-driver/mysql for MySQL/MariaDB), so it can
// return typed columns, paginate and sort server-side, filter with real bound
// parameters (never string-concatenated SQL), and apply inline cell edits inside
// one transaction keyed on the table's primary key.
//
// The DSN is derived from the running project, never taken from the request:
//   - ddev  -> `ddev describe -j` publishes the DB on a host port with creds
//   - compose/sail/native -> keel's own convention: the stack publishes the DB
//     on 127.0.0.1:<localPort> with db/db/db, overridable by the project's .env
//
// so the grid talks to the same database the project runs against, and the only
// thing the browser supplies is the (validated) table name, page, sort and a
// bound filter value.
package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/coullworks/keel/internal/engine"

	// Registered database/sql drivers. Each registers itself in its init(): the
	// mysql driver as "mysql", pgx's stdlib shim as "pgx".
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// dbEngine is the family of the project's database: it decides the driver, the
// identifier quoting, the placeholder style, and the introspection queries.
type dbEngine string

const (
	enginePostgres dbEngine = "postgres"
	engineMySQL    dbEngine = "mysql"
)

// dsn is a resolved, host-reachable connection for a project's database. It is
// derived from the project, never from the HTTP request, so a caller cannot
// point the grid at an arbitrary server.
type dsn struct {
	Engine   dbEngine
	Driver   string // database/sql driver name ("pgx" | "mysql")
	Host     string
	Port     string
	User     string
	Password string
	Name     string
}

// string renders the driver-specific DSN. Passwords land only in the connection
// string handed to the local driver, never in any response.
func (d dsn) string() string {
	switch d.Engine {
	case enginePostgres:
		return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
			d.Host, d.Port, d.User, d.Password, d.Name)
	default: // mysql
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&timeout=5s&readTimeout=15s",
			d.User, d.Password, d.Host, d.Port, d.Name)
	}
}

// engineFor decides Postgres vs MySQL from every signal keel has, not the recipe
// ids alone. A Next.js project on hosted Supabase carries no "postgres" recipe —
// it speaks Postgres by virtue of its env (a SUPABASE_* key, or a DATABASE_URL
// whose scheme is postgres) — so string-matching recipe ids would wrongly hand
// it a MySQL driver. Order of trust: an explicit Supabase/Postgres env signal, a
// Postgres-only framework, then the recipe ids, and MySQL only as the fallback.
func engineFor(m *engine.Manifest, envGet func(string) string) dbEngine {
	if envIndicatesPostgres(envGet) {
		return enginePostgres
	}
	if frameworkIsPostgres(m.Framework) {
		return enginePostgres
	}
	for _, id := range m.Recipes {
		if strings.Contains(id, "postgres") || strings.Contains(id, "supabase") {
			return enginePostgres
		}
	}
	return engineMySQL
}

// envIndicatesPostgres reports whether the project's env names Supabase or a
// Postgres DATABASE_URL. These are the hosted-stack signals a recipe id misses:
// a Node/Next project points at its database through the env, not a keel db
// recipe.
func envIndicatesPostgres(envGet func(string) string) bool {
	if envGet == nil {
		return false
	}
	for _, k := range []string{"NEXT_PUBLIC_SUPABASE_URL", "SUPABASE_URL", "SUPABASE_PROJECT_REF", "SUPABASE_DB_URL"} {
		if strings.TrimSpace(envGet(k)) != "" {
			return true
		}
	}
	if u := strings.TrimSpace(envGet("DATABASE_URL")); u != "" {
		if strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") {
			return true
		}
	}
	return false
}

// frameworkIsPostgres reports whether a framework defaults to Postgres when it
// carries no db recipe. Supabase is the Next.js data layer, so a supabase-backed
// Next project is Postgres even without a keel db recipe naming it.
func frameworkIsPostgres(fw string) bool {
	switch fw {
	case "nextjs", "next", "supabase":
		return true
	}
	return false
}

// defaultHostPort is the loopback host port keel's compose/native envs publish
// the database on (see recipes/infra/db/*.yaml db.localPort). Used when the
// project's .env doesn't override it.
func defaultHostPort(e dbEngine) string {
	if e == enginePostgres {
		return "55432"
	}
	return "33306"
}

// supabaseLocalPort is the loopback port `supabase start` publishes the local
// Postgres on. It is the fallback for a Supabase project whose env names the
// hosted URL but that is actually being run against the local stack.
const supabaseLocalPort = "54322"

// dbOpTimeout bounds a single structured DB operation (connect + a paginated
// grid page, a table list, or a small edit batch). These are all inherently
// fast, bounded queries, so a request that outlives this is a hung or wedged
// database, not slow work — and letting it hang would leave the grid on a
// spinner that never resolves. A raw SQL statement the user types (handleDBQuery)
// is deliberately NOT bounded here: it can be a legitimate long-running DDL or
// EXPLAIN, and it streams, so the browser's Stop cancels it instead.
const dbOpTimeout = 20 * time.Second

// dbCtx derives the bounded context every structured DB handler shares, so a
// wedged database surfaces as a clear "not reachable / query failed" error the
// SPA renders inline rather than an endless spinner. The returned cancel MUST be
// deferred by the caller.
func dbCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, dbOpTimeout)
}

// ddevDescribe is the seam for shelling to `ddev describe -j`. Kept as a package
// var so tests can supply canned JSON without ddev or Docker present. It runs
// argv directly (no shell), the dir is a validated tracked project, and the args
// are constant — no request data reaches the command line.
var ddevDescribe = func(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ddev", "describe", "-j")
	cmd.Dir = dir
	return cmd.Output()
}

// deriveDSN resolves a host-reachable connection for the project in dir. execCmd
// is the env recipe's `exec` prefix (ddev.../docker compose.../sail...); envGet
// reads a key from the project's .env (empty string if absent). The .env wins
// where present, because that is where a real project's own credentials live.
func deriveDSN(ctx context.Context, dir, execCmd string, m *engine.Manifest, envGet func(string) string) (dsn, error) {
	eng := engineFor(m, envGet)
	d := dsn{Engine: eng, Driver: driverFor(eng)}

	if strings.HasPrefix(strings.TrimSpace(execCmd), "ddev") {
		out, err := ddevDescribe(ctx, dir)
		if err != nil {
			return dsn{}, fmt.Errorf("ddev describe failed (is the project started?): %w", err)
		}
		return dsnFromDdev(out, eng)
	}

	// A hosted-Supabase project points its whole connection through the env, not
	// a keel db recipe, so it never matches the compose defaults below. Resolve it
	// from the Supabase env first; if that yields a full connection, use it.
	if sd, ok := deriveSupabaseDSN(envGet); ok {
		return sd, nil
	}

	// compose / sail / native: keel's convention is db/db/db on a loopback host
	// port, overridable by the project's own .env. DATABASE_URL (a full DSN, used
	// by Node/Python stacks) takes precedence when present. A Postgres project
	// whose env only names Supabase (no explicit port) falls back to the local
	// `supabase start` port rather than keel's own compose port.
	port := defaultHostPort(eng)
	if eng == enginePostgres && envIndicatesPostgres(envGet) {
		port = supabaseLocalPort
	}
	d.Host, d.Port, d.User, d.Password, d.Name = "127.0.0.1", port, "db", "db", "db"
	applyEnvOverrides(&d, envGet)
	return d, nil
}

// deriveSupabaseDSN builds a connection from Supabase env keys. Supabase exposes
// its Postgres directly through SUPABASE_DB_URL (a full DSN) and its API through
// the project URL; only the DB URL carries a reachable host+creds, so that is
// what makes a complete connection. It reports ok=false when the env names
// Supabase but carries no usable DB URL, so the caller falls back to the local
// stack port rather than a half-formed DSN.
func deriveSupabaseDSN(envGet func(string) string) (dsn, bool) {
	if envGet == nil {
		return dsn{}, false
	}
	raw := strings.TrimSpace(envGet("SUPABASE_DB_URL"))
	if raw == "" {
		// A DATABASE_URL that is explicitly Postgres is the Supabase pooled/direct
		// connection for a hosted project; honour it as the DB URL.
		if u := strings.TrimSpace(envGet("DATABASE_URL")); strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") {
			raw = u
		}
	}
	if raw == "" {
		return dsn{}, false
	}
	parsed, ok := parseDatabaseURL(raw)
	if !ok || parsed.Host == "" {
		return dsn{}, false
	}
	d := dsn{Engine: enginePostgres, Driver: driverFor(enginePostgres),
		Host: parsed.Host, Port: parsed.Port, User: parsed.User, Password: parsed.Password, Name: parsed.Name}
	if d.Port == "" {
		d.Port = "5432" // hosted Supabase Postgres default
	}
	if d.User == "" {
		d.User = "postgres"
	}
	if d.Name == "" {
		d.Name = "postgres"
	}
	return d, true
}

// applyEnvOverrides layers a project's .env onto the derived defaults. It reads
// DATABASE_URL first (a complete override) then the discrete DB_* keys, so a
// project that renamed its database or moved its port is still reachable.
func applyEnvOverrides(d *dsn, envGet func(string) string) {
	if envGet == nil {
		return
	}
	if u := strings.TrimSpace(envGet("DATABASE_URL")); u != "" {
		if parsed, ok := parseDatabaseURL(u); ok {
			if parsed.Host != "" {
				d.Host = parsed.Host
			}
			if parsed.Port != "" {
				d.Port = parsed.Port
			}
			if parsed.User != "" {
				d.User = parsed.User
			}
			if parsed.Password != "" {
				d.Password = parsed.Password
			}
			if parsed.Name != "" {
				d.Name = parsed.Name
			}
		}
	}
	set := func(dst *string, key string) {
		if v := strings.TrimSpace(envGet(key)); v != "" {
			*dst = v
		}
	}
	// A DB host of "db"/"mysql"/"postgres" is a container service name, only
	// resolvable inside the compose network - the studio runs on the host, so we
	// keep 127.0.0.1 rather than a name the host can't reach.
	if h := strings.TrimSpace(envGet("DB_HOST")); h != "" && !isContainerHost(h) {
		d.Host = h
	}
	set(&d.Port, "DB_PORT")
	set(&d.User, "DB_USERNAME")
	set(&d.User, "DB_USER")
	set(&d.Password, "DB_PASSWORD")
	set(&d.Name, "DB_DATABASE")
	set(&d.Name, "DB_NAME")
}

// isContainerHost reports whether a DB_HOST names an in-network compose service
// rather than a host-reachable address. Inside compose the app talks to "db";
// the studio, on the host, must use the published loopback port instead.
func isContainerHost(h string) bool {
	switch h {
	case "db", "mysql", "mariadb", "postgres", "postgresql", "database":
		return true
	}
	return false
}

// parseDatabaseURL pulls the parts out of a scheme://user:pass@host:port/name
// URL without the driver-specific baggage. Returns ok=false on anything it can't
// split, so callers keep their defaults.
func parseDatabaseURL(raw string) (dsn, bool) {
	i := strings.Index(raw, "://")
	if i < 0 {
		return dsn{}, false
	}
	rest := raw[i+3:]
	var out dsn
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		cred := rest[:at]
		rest = rest[at+1:]
		if colon := strings.Index(cred, ":"); colon >= 0 {
			out.User, out.Password = cred[:colon], cred[colon+1:]
		} else {
			out.User = cred
		}
	}
	// strip query/params
	if q := strings.IndexAny(rest, "?"); q >= 0 {
		rest = rest[:q]
	}
	hostPort := rest
	if slash := strings.Index(rest, "/"); slash >= 0 {
		hostPort = rest[:slash]
		out.Name = strings.TrimPrefix(rest[slash:], "/")
	}
	if colon := strings.LastIndex(hostPort, ":"); colon >= 0 {
		out.Host, out.Port = hostPort[:colon], hostPort[colon+1:]
	} else {
		out.Host = hostPort
	}
	if out.Host == "" && out.Name == "" {
		return dsn{}, false
	}
	return out, true
}

// ddevDescribeJSON is the slice of `ddev describe -j` we read: the DB info under
// raw.dbinfo. ddev publishes the DB on a host port and reports it here, so the
// studio (on the host) connects to that rather than the in-container service.
// published_port is a JSON number in ddev's output; json.Number reads it as a
// string without imposing an int type.
type ddevDescribeJSON struct {
	Raw struct {
		DBInfo struct {
			Host          string      `json:"host"`
			DBPort        string      `json:"dbPort"`
			PublishedPort json.Number `json:"published_port"`
			Username      string      `json:"username"`
			Password      string      `json:"password"`
			DBName        string      `json:"dbname"`
		} `json:"dbinfo"`
	} `json:"raw"`
}

// dsnFromDdev builds a host-reachable DSN from `ddev describe -j` output.
func dsnFromDdev(jsonOut []byte, eng dbEngine) (dsn, error) {
	var parsed ddevDescribeJSON
	if err := json.Unmarshal(jsonOut, &parsed); err != nil {
		return dsn{}, fmt.Errorf("could not parse ddev describe output: %w", err)
	}
	info := parsed.Raw.DBInfo
	d := dsn{Engine: eng, Driver: driverFor(eng), Host: "127.0.0.1"}
	d.Port = firstNonEmpty(info.PublishedPort.String(), info.DBPort)
	d.User = firstNonEmpty(info.Username, "db")
	d.Password = firstNonEmpty(info.Password, "db")
	d.Name = firstNonEmpty(info.DBName, "db")
	if d.Port == "" {
		return dsn{}, fmt.Errorf("ddev did not report a published database port (is it running?)")
	}
	return d, nil
}

func driverFor(e dbEngine) string {
	if e == enginePostgres {
		return "pgx"
	}
	return "mysql"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// --- query building ----------------------------------------------------------

// gridQuery is a validated request for a page of a table. Everything a caller
// controls is here; the SQL that gets built from it binds Filter as a parameter
// and only ever interpolates identifiers it has verified against the live
// schema, never raw request text.
type gridQuery struct {
	Table     string
	Page      int // 1-based
	PageSize  int
	SortCol   string // must be a real column; validated by caller
	SortDesc  bool
	FilterCol string // must be a real column; validated by caller
	Filter    string // bound as a parameter (LIKE), never concatenated
}

const (
	defaultPageSize = 50
	maxPageSize     = 500
)

// normalise clamps the paging inputs to sane bounds so a caller can't ask for
// page -3 or a million rows.
func (q *gridQuery) normalise() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = defaultPageSize
	}
	if q.PageSize > maxPageSize {
		q.PageSize = maxPageSize
	}
}

// placeholder returns the engine's positional placeholder for the n-th (1-based)
// bound parameter: $1.. for Postgres, ? for MySQL.
func placeholder(e dbEngine, n int) string {
	if e == enginePostgres {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}

// quoteIdent quotes a validated identifier for the engine. It is only ever
// called on a name already checked against the live schema (a table or column
// the database reported), and it still doubles the quote char as defence in
// depth so a pathological identifier can't break out.
func quoteIdent(e dbEngine, name string) string {
	if e == enginePostgres {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// buildSelect renders the paged SELECT and its bound args. cols is the verified
// column list (so we never SELECT *); the WHERE, when present, binds the filter
// value as a parameter. Sort and filter columns must already be validated by the
// caller against cols.
func buildSelect(e dbEngine, q gridQuery, cols []string) (string, []any) {
	var b strings.Builder
	b.WriteString("SELECT ")
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(e, c)
	}
	b.WriteString(strings.Join(quoted, ", "))
	b.WriteString(" FROM ")
	b.WriteString(quoteIdent(e, q.Table))

	var args []any
	if q.FilterCol != "" && q.Filter != "" {
		b.WriteString(" WHERE ")
		b.WriteString(castTextForLike(e, q.FilterCol))
		b.WriteString(" LIKE ")
		b.WriteString(placeholder(e, 1))
		args = append(args, "%"+q.Filter+"%")
	}
	if q.SortCol != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(quoteIdent(e, q.SortCol))
		if q.SortDesc {
			b.WriteString(" DESC")
		} else {
			b.WriteString(" ASC")
		}
	}
	// LIMIT/OFFSET are integers we clamped ourselves, so they are safe to inline
	// (many drivers won't bind LIMIT placeholders anyway).
	offset := (q.Page - 1) * q.PageSize
	fmt.Fprintf(&b, " LIMIT %d OFFSET %d", q.PageSize, offset)
	return b.String(), args
}

// castTextForLike wraps the filtered column so a LIKE works against non-text
// columns (ints, timestamps) without the caller needing per-type logic.
func castTextForLike(e dbEngine, col string) string {
	q := quoteIdent(e, col)
	if e == enginePostgres {
		return q + "::text"
	}
	return "CAST(" + q + " AS CHAR)"
}

// buildCount renders the COUNT(*) that backs the grid's total, with the same
// bound filter as the page query so the pager reflects the filtered set.
func buildCount(e dbEngine, q gridQuery) (string, []any) {
	var b strings.Builder
	b.WriteString("SELECT COUNT(*) FROM ")
	b.WriteString(quoteIdent(e, q.Table))
	var args []any
	if q.FilterCol != "" && q.Filter != "" {
		b.WriteString(" WHERE ")
		b.WriteString(castTextForLike(e, q.FilterCol))
		b.WriteString(" LIKE ")
		b.WriteString(placeholder(e, 1))
		args = append(args, "%"+q.Filter+"%")
	}
	return b.String(), args
}

// --- update building ---------------------------------------------------------

// cellEdit is one staged inline edit: set Column to Value in the row whose
// primary key columns equal Key. Value/Key are bound as parameters.
type cellEdit struct {
	Column string            `json:"column"`
	Value  *string           `json:"value"` // nil -> SQL NULL
	Key    map[string]string `json:"key"`   // pk column -> value
}

// buildUpdate renders one parameterised UPDATE for a staged edit. pk is the
// verified primary-key column set; col must be a verified column. It returns an
// error rather than an unkeyed UPDATE if the edit's key doesn't cover the pk, so
// a bad edit can never rewrite every row.
func buildUpdate(e dbEngine, table, col string, value *string, key map[string]string, pk []string) (string, []any, error) {
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("table %q has no primary key: refusing to edit", table)
	}
	var b strings.Builder
	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(e, table))
	b.WriteString(" SET ")
	b.WriteString(quoteIdent(e, col))
	b.WriteString(" = ")

	var args []any
	n := 1
	b.WriteString(placeholder(e, n))
	n++
	if value == nil {
		args = append(args, nil)
	} else {
		args = append(args, *value)
	}

	b.WriteString(" WHERE ")
	for i, k := range pk {
		v, ok := key[k]
		if !ok {
			return "", nil, fmt.Errorf("edit is missing primary-key column %q", k)
		}
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(quoteIdent(e, k))
		b.WriteString(" = ")
		b.WriteString(placeholder(e, n))
		n++
		args = append(args, v)
	}
	return b.String(), args, nil
}

// --- driver-side execution ---------------------------------------------------

// column is a grid column: its name and its database type, so the UI can render
// typed cells and right-align numbers. FK, when set, names the table and column
// this column references, so the UI can turn the cell into a link that loads the
// referenced row.
type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
	FK   *fkRef `json:"fk,omitempty"`
}

// fkRef is the target of a foreign key: the referenced table and its column.
// Both come straight from the live schema, so they are safe to interpolate as
// identifiers (the FK-follow query still binds the value as a parameter).
type fkRef struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// gridResult is the structured payload the grid endpoints return: typed columns,
// string-encoded cells (nil -> null), the primary key so the UI knows which
// cells are editable, and the paging envelope.
type gridResult struct {
	Table    string      `json:"table"`
	Columns  []column    `json:"columns"`
	Rows     [][]*string `json:"rows"`
	PK       []string    `json:"pk"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
	Total    int         `json:"total"`
}

// openDB is the seam for opening a real connection. Tests swap it for an
// in-memory fake so no network or database is needed. The default opens the
// registered driver and pings with the request context.
var openDB = func(ctx context.Context, d dsn) (*sql.DB, error) {
	db, err := sql.Open(d.Driver, d.string())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// listColumns returns the table's columns (name + type) in ordinal order,
// straight from information_schema so it reflects the live table.
func listColumns(ctx context.Context, db *sql.DB, e dbEngine, table string) ([]column, error) {
	var q string
	if e == enginePostgres {
		q = `SELECT column_name, data_type FROM information_schema.columns
		     WHERE table_schema = 'public' AND table_name = $1 ORDER BY ordinal_position`
	} else {
		q = `SELECT column_name, data_type FROM information_schema.columns
		     WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`
	}
	rows, err := db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// primaryKey returns the table's primary-key columns (empty if it has none, in
// which case the row is read-only). This is what makes inline edit safe: an
// UPDATE is keyed on these, so it touches exactly one row.
func primaryKey(ctx context.Context, db *sql.DB, e dbEngine, table string) ([]string, error) {
	var q string
	if e == enginePostgres {
		q = `SELECT a.attname
		     FROM pg_index i
		     JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		     WHERE i.indrelid = ($1)::regclass AND i.indisprimary
		     ORDER BY array_position(i.indkey, a.attnum)`
	} else {
		q = `SELECT column_name FROM information_schema.key_column_usage
		     WHERE table_schema = DATABASE() AND table_name = ? AND constraint_name = 'PRIMARY'
		     ORDER BY ordinal_position`
	}
	rows, err := db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pk []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		pk = append(pk, name)
	}
	return pk, rows.Err()
}

// foreignKeys returns the table's foreign keys as a map of local column ->
// referenced (table, column), straight from information_schema so it reflects
// the live schema. Only single-column FKs are followed: a composite FK has no
// single value to link on, so the grid leaves those columns plain. The
// referenced table/column names are real identifiers the database reported, so
// the UI can safely open them; the value it links by is still bound as a
// parameter on the follow query.
func foreignKeys(ctx context.Context, db *sql.DB, e dbEngine, table string) (map[string]fkRef, error) {
	var q string
	if e == enginePostgres {
		q = `SELECT kcu.column_name, ccu.table_name, ccu.column_name
		     FROM information_schema.table_constraints tc
		     JOIN information_schema.key_column_usage kcu
		       ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
		     JOIN information_schema.constraint_column_usage ccu
		       ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
		     WHERE tc.constraint_type = 'FOREIGN KEY'
		       AND tc.table_schema = 'public' AND tc.table_name = $1`
	} else {
		q = `SELECT column_name, referenced_table_name, referenced_column_name
		     FROM information_schema.key_column_usage
		     WHERE table_schema = DATABASE() AND table_name = ?
		       AND referenced_table_name IS NOT NULL`
	}
	rows, err := db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Count how many local columns each constraint touches so composite keys can
	// be dropped: a two-column FK reports two rows sharing the local column set,
	// and a single value can't identify the referenced row.
	type ref struct{ table, col string }
	seen := map[string]int{}
	refs := map[string]ref{}
	for rows.Next() {
		var local, refTable, refCol string
		if err := rows.Scan(&local, &refTable, &refCol); err != nil {
			return nil, err
		}
		seen[local]++
		refs[local] = ref{refTable, refCol}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]fkRef{}
	for local, r := range refs {
		if seen[local] == 1 { // single-column FK only
			out[local] = fkRef{Table: r.table, Column: r.col}
		}
	}
	return out, nil
}

// attachFKs marks each column with its foreign-key target, so the grid JSON
// carries the link the UI needs. FK detection failing is non-fatal: the grid
// still renders, just without clickable FK cells.
func attachFKs(ctx context.Context, db *sql.DB, e dbEngine, table string, cols []column) []column {
	fks, err := foreignKeys(ctx, db, e, table)
	if err != nil || len(fks) == 0 {
		return cols
	}
	for i := range cols {
		if ref, ok := fks[cols[i].Name]; ok {
			r := ref
			cols[i].FK = &r
		}
	}
	return cols
}

// tableExists validates a caller-supplied table name against the live schema.
// Every identifier the studio interpolates into SQL passes through here or the
// column list first, so a name from the request can never be an injection
// vector: it either matches a real table or the request is refused.
func tableExists(ctx context.Context, db *sql.DB, e dbEngine, table string) (bool, error) {
	var q string
	if e == enginePostgres {
		q = `SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`
	} else {
		q = `SELECT 1 FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?`
	}
	var one int
	err := db.QueryRowContext(ctx, q, table).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// canonicalTable returns the table's own name as the schema records it, matched
// against the requested name as a bound parameter. The returned name is a
// database read, so a caller threads a trusted identifier into SQL rather than
// the request string. Reports false if the table does not exist.
func canonicalTable(ctx context.Context, db *sql.DB, e dbEngine, table string) (string, bool, error) {
	var q string
	if e == enginePostgres {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`
	} else {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?`
	}
	var name string
	err := db.QueryRowContext(ctx, q, table).Scan(&name)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// scanRows reads a result set into string-encoded cells (nil -> SQL NULL), which
// is a lossless, driver-agnostic transport for a grid: every value round-trips
// as text and the UI decides how to render it by column type.
func scanRows(rows *sql.Rows) ([][]*string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := [][]*string{}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]*string, len(cols))
		for i, v := range raw {
			row[i] = toCell(v)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// toCell encodes one scanned value as a *string (nil for NULL). []byte (how the
// drivers hand back text/blob) becomes a string; everything else uses Sprint.
func toCell(v any) *string {
	if v == nil {
		return nil
	}
	var s string
	switch t := v.(type) {
	case []byte:
		s = string(t)
	case string:
		s = t
	default:
		s = fmt.Sprint(t)
	}
	return &s
}

// validCol reports whether name is one of the table's real columns. Used to
// gate the sort/filter/edit columns a caller supplies before they reach SQL.
func validCol(cols []column, name string) bool {
	for _, c := range cols {
		if c.Name == name {
			return true
		}
	}
	return false
}

func colNames(cols []column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// canonicalCol returns the table's own column name matching name, taken from the
// schema in cols (a database read) rather than the request. Callers thread the
// returned value into SQL so the identifier provably originates from the schema,
// not user input. Reports false if name is not a real column.
func canonicalCol(cols []column, name string) (string, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c.Name, true
		}
	}
	return "", false
}
