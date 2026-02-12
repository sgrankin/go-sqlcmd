# sqlcmd Improvements for Query Analysis Workflow

Improvements discovered while profiling demand planning queries against Azure SQL.
See `~/Code/demand-planning-query-analysis.md` for the session that motivated these.

## 1. Read-only mode (on by default)

**Problem**: It's too easy to accidentally run a destructive statement (UPDATE, DELETE, DROP) when exploring a production or shared dev database.

**Proposal**: Add a `--read-only` flag (default: on) that rejects non-read statements before sending them to the server. Parse each batch and only allow:
- `SELECT`, `WITH ... SELECT`
- `SET` (session options like STATISTICS, NOCOUNT, etc.)
- `DECLARE`, `PRINT`
- `EXEC` / `EXECUTE` only with an explicit allowlist or `--allow-exec` flag

Reject everything else (`INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `CREATE`, `TRUNCATE`, `MERGE`, `EXEC` by default) with a clear error message and a hint to use `--read-only=false` or `--rw`.

This is a client-side safety net, not a security boundary. It protects against typos and copy-paste errors, not malicious intent.

**Open questions**:
- Should `BEGIN TRAN` / `ROLLBACK` be allowed? Useful for "what-if" exploration, but risky if the user forgets to rollback.
- Should temp table creation (`CREATE TABLE #foo`, `SELECT INTO #foo`) be allowed? These are common in analysis workflows and don't affect real data.
- Could also set `ApplicationIntent=ReadOnly` on the connection string to route to read replicas on Azure SQL (orthogonal to statement filtering, but complementary).

## 2. Proper query plan output

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

## 3. Built-in plan analysis

**Problem**: Analyzing an XML execution plan requires external scripts (currently ~257 lines of Python across 4 files). This means a Python dependency and manual file juggling.

**Proposal**: Add a `sqlcmd plan analyze <file.xml>` subcommand (or `--plan --analyze`) that produces:
- Statement metadata (elapsed, CPU, memory grant, DOP, compile time, optimizer warnings)
- Operator tree with estimated vs actual rows and elapsed time per node
- Top cardinality estimation errors
- Operator-level warnings and missing statistics

**Go vs Python for this**: Go is fine. The analysis is just XML tree walking — `encoding/xml` handles the ShowPlan namespace cleanly. The main advantage of Go is that it's a single binary with no runtime dependency. The Python scripts are simple enough that a direct port is straightforward (~400 lines of Go, less than the Python once you account for Go's XML unmarshalling structs being reusable).

The ShowPlan XML schema is well-documented by Microsoft, so typed Go structs would actually be an improvement over Python's string-based attribute access.

**Known bug in current Python scripts**: The operator tree only reads `ActualRows`/`ActualElapsedms` from the first `RunTimeCountersPerThread` element. For parallel plans (DOP > 1), there are multiple thread entries. Correct handling: **sum** `ActualRows` across all threads (rows are partitioned), **max** `ActualElapsedms` (threads run concurrently). The Go port should get this right from the start.

### Output format

Default to human-readable text (matching current Python script output). Consider also supporting `--format json` for machine consumption (e.g., for CI pipelines comparing plan metrics across commits).

## 4. Machine-readable output formats

**Problem**: The default tabular output is lossy (truncation, alignment ambiguity) and not queryable. For analysis workflows, you want to run follow-up queries against results without re-hitting the server.

**Proposal**: Add `--format` options:

```bash
# CSV — universal interchange, DuckDB reads it natively
sqlcmd --profile dev --format csv -Q "SELECT ..." -o /tmp/results.csv
duckdb -c "SELECT * FROM '/tmp/results.csv' WHERE Demand > 100"

# JSONL — one JSON object per row, self-describing, no truncation
sqlcmd --profile dev --format jsonl -Q "SELECT ..." -o /tmp/results.jsonl
duckdb -c "SELECT * FROM read_json('/tmp/results.jsonl') WHERE Demand > 100"
```

sqlcmd already has JSON/YAML formatters in `internal/output/formatter/`. JSONL and CSV should be small extensions.

**Key design point**: Don't build a query engine into sqlcmd. Output clean structured data and let DuckDB (or sqlite3, jq, pandas, etc.) handle analytical queries on the result. DuckDB's `read_csv_auto()` and `read_json_auto()` handle schema inference, so the output just needs to be correct, not clever.

**Type fidelity**: CSV loses types (everything is a string). JSONL preserves numbers vs strings vs nulls. For most analysis workflows CSV is fine since DuckDB infers types well, but JSONL is better when exact types matter (e.g., distinguishing 0 from NULL, or decimal precision).

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

## Priority

1. **Read-only mode** — safety first, prevents accidents
2. **`--plan` flag** — biggest workflow friction point today
3. **`--format csv/jsonl`** — enables DuckDB-based analysis workflows
4. **Plan analysis** — nice to have, Python scripts work fine as stopgap
5. **Better defaults** — small quality of life
6. **Connection profiles** — convenience, workaround is shell aliases
7. **MCP server mode** — future, when the above are solid
