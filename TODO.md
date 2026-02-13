# sqlcmd Improvements for Query Analysis Workflow

Improvements discovered while profiling demand planning queries against Azure SQL.
See `~/Code/demand-planning-query-analysis.md` for the session that motivated these.

## 1. ~~Read-only mode (on by default)~~ DONE

Implemented in `pkg/sqlcmd/readonly.go`. Client-side safety net that rejects destructive SQL before it reaches the server.

- **Default ON** — use `--rw` to disable
- **`--allow-exec`** — permits EXEC/EXECUTE in read-only mode
- **BEGIN TRAN / COMMIT / ROLLBACK**: Rejected (decision: too risky if user forgets to rollback)
- **Temp tables**: Allowed (`CREATE TABLE #foo`, `SELECT INTO #foo`) — common in analysis, no real data impact
- **CTEs**: `WITH...SELECT` allowed, `WITH...DELETE/UPDATE/INSERT/MERGE` rejected
- Wired into both legacy (`internal/legacy`) and modern (`cmd/sqlcmd/root/query.go`) CLI paths
- `ApplicationIntent=ReadOnly` for read replicas is orthogonal and not yet implemented

## 2. ~~Proper query plan output~~ DONE (2a)

**Problem**: When `SET STATISTICS XML ON` is active, the execution plan XML is returned as a regular result column. sqlcmd's column-width wrapping (`-w 65535`) splits the XML across lines, producing invalid XML that requires manual reassembly:

```python
# Current workaround: rejoin lines and regex-extract
content = open('output.txt').read().replace('\n', '')
match = re.search(r'(<ShowPlanXML.*?</ShowPlanXML>)', content)
```

**Proposal**: Two improvements:

### 2a. `--plan` flag

```bash
sqlcmd --plan -i query.sql -o plan.xml
sqlcmd --plan -Q "SELECT * FROM foo" -o plan.xml
```

Wraps the query in `SET STATISTICS XML ON` / `OFF`, suppresses the data result set, and writes only the clean XML plan to the output file. This is the common case for performance analysis.

### 2b. Detect and handle XML plan columns

When outputting normally (no `--plan` flag), detect result columns containing `<ShowPlanXML` and:
- Write the XML as a single unwrapped line (ignore `-w` for this column)
- Or write it to a sidecar file (`output.plan.xml`) and print a reference in the output

## 3. ~~Built-in plan analysis~~ DONE

Ported Python plan analysis scripts into native Go in `internal/plan/`. Three usage modes:

```bash
# Analyze a saved plan file
sqlcmd plan analyze plan.xml
sqlcmd plan analyze --format json plan.xml

# Run a query and analyze its plan (no data output)
sqlcmd plan analyze -Q "SELECT * FROM orders" --database mydb

# Normal query + analysis appended
sqlcmd query --analyze "SELECT * FROM orders"
sqlcmd query --analyze --plan-file plan.xml "SELECT ..."

# Legacy CLI
sqlcmd -Q "SELECT ..." --analyze
sqlcmd -Q "SELECT ..." --analyze --plan-file plan.xml
```

**Analysis output includes**:
- Statement metadata (cost, optimization level, set options)
- QueryPlan attributes (DOP, cached plan size, compile time/memory)
- MemoryGrantInfo, HardwareDependentProperties
- Plan-level warnings (e.g., PlanAffectingConvert)
- QueryTimeStats (CPU, elapsed)
- Optimizer statistics usage count
- Full operator tree with estimated/actual rows and elapsed time
- Top 15 cardinality estimation errors sorted by ratio
- Operator-level warnings (e.g., ColumnsWithNoStatistics)
- Missing statistics

**Implementation**: `internal/plan/` is a pure analysis package (no SQL connections, no CLI concerns):
- `parse.go` — streaming XML parser, CSV unwrapping, multi-document support
- `analyze.go` — parallel thread aggregation (SUM rows, MAX elapsed), cardinality errors
- `format_text.go` / `format_json.go` — output formatters
- `plan_test.go` — 14 tests at 84% coverage against embedded XML fixtures

**Fixed Python bug**: Parallel plans correctly aggregate across all `RunTimeCountersPerThread` elements (SUM `ActualRows`, MAX `ActualElapsedms`).

## 4. ~~Machine-readable output formats~~ DONE

Implemented `--format csv` and `--format jsonl` for both modern CLI (`sqlcmd query --format csv`) and legacy CLI (`sqlcmd -Q "..." --format csv`).

- **CSV**: `encoding/csv` writer, column names as header row, NULL → empty string, messages/errors to stderr
- **JSONL**: One JSON object per line, type-preserving (numbers as numbers, bools as bools, nulls as JSON null, dates as ISO strings, binary as `0x...` hex)
- **Implementation**: `pkg/sqlcmd/format_csv.go`, `pkg/sqlcmd/format_jsonl.go` implement the `Formatter` interface
- Shared helpers extracted: `scanRowStrings()`, `scanRowTyped()`, `formatTime()` in `format.go`

```bash
# CSV output (modern CLI)
sqlcmd query "SELECT name, database_id FROM sys.databases" --format csv

# CSV output (legacy CLI)
sqlcmd -Q "SELECT name, database_id FROM sys.databases" --format csv

# JSONL output — preserves types (numbers, bools, nulls)
sqlcmd query "SELECT name, database_id FROM sys.databases" --format jsonl

# Save to file and query with DuckDB
sqlcmd -Q "SELECT * FROM large_table" --format csv -o /tmp/results.csv
duckdb -c "SELECT * FROM '/tmp/results.csv' WHERE amount > 100"

# JSONL to file, query with DuckDB
sqlcmd -Q "SELECT * FROM large_table" --format jsonl -o /tmp/results.jsonl
duckdb -c "SELECT * FROM read_json('/tmp/results.jsonl') WHERE amount > 100"

# JSONL piped to jq
sqlcmd query "SELECT name, database_id FROM sys.databases" --format jsonl | jq '.name'

# Combine with --plan-file for analysis workflows
sqlcmd -Q "SELECT * FROM orders" --format csv -o results.csv --plan-file plan.xml
```

**Design notes**:
- Messages like "(N rows affected)" go to stderr, keeping stdout clean for piping
- CSV NULL values are empty fields (DuckDB's `read_csv_auto()` handles this correctly)
- JSONL NULL values are JSON `null` (unambiguous)
- Multiple result sets in one query produce consecutive output (CSV: each with its own header row; JSONL: consecutive objects)

## 5. ~~Better defaults for large output~~ DONE

When output goes to a file (`-o`) and `-y` is not explicitly set, defaults to `-y 0` (unlimited variable-length column display). Interactive/terminal output keeps the current default of 256 to avoid flooding the screen. Implemented in `internal/legacy/sqlcmd.go` Execute() function.

## 6. Auto-detect Azure AD auth for Azure SQL

**Problem**: Connecting to Azure SQL always requires `--authentication-method=ActiveDirectoryDefault`, which is boilerplate since there's no other sensible default for `*.database.windows.net`.

**Proposal**: When the server name matches `*.database.windows.net` and no `--authentication-method` is explicitly set, default to `ActiveDirectoryDefault`. All other servers keep current behavior (SQL auth / Windows auth).

## ~~7. Connection profiles~~ DROPPED

Not worth it — connection strings are just server + database name, short enough to type or alias.

## 7. ~~MCP server mode~~ DROPPED

Not worth the complexity. The CLI with `--format jsonl/csv` and `--plan-file` already works well for programmatic use from Claude Code or scripts. Shell invocation is fine.

## 8. ~~Test infrastructure: testcontainers-go~~ DONE

Implemented in `internal/sqlservertest/`. Uses testcontainers-go MSSQL module with a named, reusable container (`go-sqlcmd-test`) shared across all test packages.

- **`SetupForTestMain()`** — called from `TestMain` in `pkg/sqlcmd`, `internal/legacy`, `cmd/sqlcmd`, `cmd/sqlcmd/root`
- **Container reuse**: `WithReuseByName` ensures all packages share one SQL Server instance during `go test ./...`
- **Colima support**: Auto-detects Docker socket from `docker context inspect`
- **Respects existing server**: If `SQLCMDSERVER` is already set, no container is started
- **Graceful degradation**: If Docker is unavailable, prints message and tests that need SQL fail individually

## 9. ~~Claude skill for query plan analysis~~ DONE

Implemented in `.claude/skills/query-plan-analysis.md`. Covers plan capture (`--plan-file`), analysis (`sqlcmd plan analyze`, `--analyze`), reading analysis output (cardinality errors, warnings, operator tree), and common optimization actions.

## Priority

1. ~~**Read-only mode**~~ — DONE
2. ~~**`--plan` flag**~~ — DONE (`--plan-file`)
3. ~~**`--format csv/jsonl`**~~ — DONE (`--format csv`, `--format jsonl`)
4. ~~**Plan analysis**~~ — DONE (`sqlcmd plan analyze`, `--analyze` flag)
5. ~~**Better defaults**~~ — DONE
6. ~~**Claude skill for plan analysis**~~ — DONE
7. **Azure AD auto-detect** — default to ActiveDirectoryDefault for *.database.windows.net, next up
8. ~~**Connection profiles**~~ — DROPPED
9. ~~**MCP server mode**~~ — DROPPED
