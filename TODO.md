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
- Wired into both legacy (`cmd/sqlcmd`) and modern (`cmd/modern/root/query.go`) CLI paths
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

## 5. Better defaults for large output

**Problem**: Default column display width truncates large values silently. We had to use `-W -w 65535 -y 0` to get full output. The `-y 0` (unlimited variable-length display) is particularly non-obvious.

**Proposal**: When output is going to a file (`-o`), default to `-y 0` (unlimited variable-length column display) unless explicitly overridden. Interactive/terminal output can keep current defaults to avoid flooding the screen.

## 6. Connection profiles

**Problem**: Connection strings are long and repetitive:
```bash
sqlcmd -S fulcrum-east2-dev.database.windows.net \
  -d rockrunindustries_20260129_sg \
  --authentication-method=ActiveDirectoryDefault
```

**Proposal**: sqlcmd already has `sqlcmd config` for container contexts. Extend it (or add a lighter-weight mechanism) for connection profiles:

```bash
sqlcmd profile add dev-rri \
  -S fulcrum-east2-dev.database.windows.net \
  -d rockrunindustries_20260129_sg \
  --authentication-method=ActiveDirectoryDefault

sqlcmd --profile dev-rri -Q "SELECT 1"
sqlcmd --profile dev-rri --plan -i query.sql -o plan.xml
```

## 7. MCP server mode (future)

Add a `sqlcmd --mcp` flag that speaks the MCP (Model Context Protocol) stdio protocol, exposing tools like `run_query` and `get_query_plan`. This would let Claude Code (or other MCP clients) call SQL queries as native tools instead of shelling out via Bash. Lower priority since Bash invocation works, but would provide better ergonomics (structured input/output, no shell escaping, direct error handling).

## 8. ~~Test infrastructure: testcontainers-go~~ DONE

Implemented in `internal/sqlservertest/`. Uses testcontainers-go MSSQL module with a named, reusable container (`go-sqlcmd-test`) shared across all test packages.

- **`SetupForTestMain()`** — called from `TestMain` in `pkg/sqlcmd`, `cmd/sqlcmd`, `cmd/modern`, `cmd/modern/root`
- **Container reuse**: `WithReuseByName` ensures all packages share one SQL Server instance during `go test ./...`
- **Colima support**: Auto-detects Docker socket from `docker context inspect`
- **Respects existing server**: If `SQLCMDSERVER` is already set, no container is started
- **Graceful degradation**: If Docker is unavailable, prints message and tests that need SQL fail individually

## Priority

1. ~~**Read-only mode**~~ — DONE
2. ~~**`--plan` flag**~~ — DONE (`--plan-file`)
3. ~~**`--format csv/jsonl`**~~ — DONE (`--format csv`, `--format jsonl`)
4. ~~**Plan analysis**~~ — DONE (`sqlcmd plan analyze`, `--analyze` flag)
5. **Better defaults** — small quality of life
6. **Connection profiles** — convenience, workaround is shell aliases
7. **MCP server mode** — future, when the above are solid
