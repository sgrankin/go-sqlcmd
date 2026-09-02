---
name: sql-profiling
description: Analyze SQL Server query plans, profile slow queries, and explore results locally with DuckDB. Use when a query is slow, needs optimization, or the user wants to understand query behavior.
allowed-tools: Bash(sqlcmd *), Bash(duckdb *), Bash(mkdir *), Read, Write, Glob, Grep
---

# Query Plan Analysis with sqlcmd

Use sqlcmd's built-in plan capture and analysis features to profile SQL Server queries.

Write all output files (plan XML, CSV, JSONL) to `/tmp/sql-profiling/` so they don't clutter the working directory.
Create it with `mkdir -p /tmp/sql-profiling` if needed.

Keep database operations read-only unless the user explicitly authorizes a write against a named target.
The `--rw` flag disables sqlcmd's read-only protection; never add it automatically.
Treat plan findings as hypotheses to verify before recommending a production change.

## Connecting to Azure SQL

For `.database.windows.net` servers, use `-G` to enable Azure AD auth (uses `ActiveDirectoryDefault`, which picks up `az login` credentials, managed identity, etc.):

```bash
sqlcmd -S myserver.database.windows.net -d mydb -G -Q "SELECT 1"
```

No `--authentication-method` flag is needed — `-G` alone defaults to `ActiveDirectoryDefault`.

## Capturing an Execution Plan

```bash
# Save plan XML to a file
sqlcmd -S server -d dbname -Q "SELECT * FROM orders WHERE id = 42" --plan-file plan.xml

# Save plan AND get query results
sqlcmd -S server -d dbname -Q "SELECT * FROM orders WHERE id = 42" --plan-file plan.xml --format csv -o results.csv

# Modern CLI equivalent
sqlcmd query --plan-file plan.xml "SELECT * FROM orders WHERE id = 42"
```

## Analyzing a Plan

```bash
# Analyze a saved plan file (text summary)
sqlcmd plan analyze plan.xml

# JSON output for programmatic use
sqlcmd plan analyze --format json plan.xml

# Write JSON to file + summary to stdout
sqlcmd plan analyze --format json -o analysis.json --summary plan.xml

# Run a query and analyze its plan in one step
sqlcmd plan analyze -Q "SELECT * FROM orders" -S server -d dbname

# Compile without executing and analyze the estimated plan
sqlcmd plan analyze -Q "SELECT * FROM orders" -S server -d dbname --estimated

# Capture + analyze in one step, write analysis to file with summary
sqlcmd -S server -d dbname -Q "SELECT * FROM orders" --analyze-file analysis.txt --summary
sqlcmd -S server -d dbname -Q "SELECT * FROM orders" --analyze-file analysis.txt --plan-file plan.xml --summary
```

Use `--estimated` when executing the query would be unsafe or unnecessarily expensive.
Estimated plans contain compile-time estimates and warnings, but no runtime row counts, elapsed time, spills, or cardinality-error measurements.

## AI-Friendly Workflow (Recommended)

Use `--summary` and file output flags to minimize context window usage:

```bash
# Full capture: data to file, plan to file, analysis to file, summary to stdout
sqlcmd -S server -d dbname \
  -Q "SELECT * FROM orders WHERE date > '2024-01-01'" \
  -o /tmp/sql-profiling/results.csv --format csv \
  --plan-file /tmp/sql-profiling/plan.xml \
  --analyze-file /tmp/sql-profiling/analysis.txt \
  --summary

# Output is concise:
# 12,847 rows, 8 columns [order_id, customer_id, amount, ...]
# Results: /tmp/sql-profiling/results.csv
# Plan: /tmp/sql-profiling/plan.xml
# Analysis: /tmp/sql-profiling/analysis.txt
# Cost: 142.3 | DOP: 4 | Elapsed: 2340ms | CPU: 8120ms
# Cardinality errors: Node 15 (847x over), Node 23 (12x under)

# Then read the analysis file for the full flat text report
```

## Reading the Analysis Output

### Statement Summary
- **EstimatedTotalSubtreeCost**: The optimizer's cost estimate. Compare across query variants.
- **StatementOptmLevel**: FULL is normal. TRIVIAL means the optimizer didn't explore alternatives.
- **SetOptions**: Verify ARITHABORT=true and ANSI settings match production (mismatches cause plan cache issues).

### QueryPlan Attributes
- **DegreeOfParallelism**: >1 means parallel execution. Not always better for OLTP.
- **CachedPlanSize**: Large plans may indicate over-complex queries.
- **CompileTime/CompileCPU/CompileMemory**: High values suggest the optimizer is struggling.

### Operator Tree
Shows the execution plan as an indented tree with:
- **EstimateRows vs ActualRows**: Large discrepancies indicate cardinality estimation errors.
- **ActualElapsedms**: Where time is actually spent.
- Look for: Table Scans (missing indexes), Hash Matches (memory-hungry joins), Sorts (could indicate missing index ordering).

### Cardinality Errors
Lists operators where estimated vs actual rows diverge most. A ratio >10x can indicate:
- **Stale statistics**: Verify statistics freshness before proposing a targeted update
- **Missing statistics**: Check the "Missing Statistics" section
- **Parameter sniffing**: The cached plan was optimized for different parameter values
- **Correlated predicates**: The optimizer assumes column independence

### Warnings
- **PlanAffectingConvert**: Implicit type conversions preventing index seeks. Fix by matching types.
- **ColumnsWithNoStatistics**: Create statistics or let auto-create handle it.
- **SpillToTempDb**: Memory grant was insufficient. May need `OPTION (MIN_GRANT_PERCENT = ...)`.
- **NoJoinPredicate**: Usually a missing WHERE/ON clause (accidental cross join).

## Common Optimization Actions

1. **Missing index**: Treat a plan suggestion as a lead; check existing indexes and write overhead before recommending it.
2. **Update statistics**: Verify that statistics are stale, then prefer a targeted update over `sp_updatestats`.
3. **Rewrite query**: Eliminate implicit conversions, simplify joins, break up complex CTEs.
4. **Compare plans**: Capture plans for two query variants and compare costs and operator trees.

## Analyzing Results with DuckDB

Use `--format jsonl` or `--format csv` to export results, then query locally with DuckDB:

```bash
# Export as JSONL (preserves types: numbers, bools, nulls)
sqlcmd -S server -d mydb -Q "SELECT * FROM large_table" --format jsonl -o results.jsonl

# Query with DuckDB
duckdb -c "SELECT * FROM read_json('results.jsonl') WHERE amount > 100"
duckdb -c "SUMMARIZE SELECT * FROM read_json('results.jsonl')"

# CSV works too
sqlcmd -S server -d mydb -Q "SELECT * FROM large_table" --format csv -o results.csv
duckdb -c "SELECT * FROM 'results.csv' WHERE amount > 100"
```

This is useful for local aggregation, filtering, or exploration of large result sets without re-querying the server.

## Typical Workflow

```bash
mkdir -p /tmp/sql-profiling

# 1. Capture everything, get summary
sqlcmd -S myserver -d mydb \
  -Q "SELECT o.*, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.date > '2024-01-01'" \
  -o /tmp/sql-profiling/results.jsonl --format jsonl \
  --plan-file /tmp/sql-profiling/plan.xml \
  --analyze-file /tmp/sql-profiling/analysis.txt \
  --summary

# 2. Explore results locally if needed
duckdb -c "SUMMARIZE SELECT * FROM read_json('/tmp/sql-profiling/results.jsonl')"

# 3. Read the flat text analysis for operator tree, cardinality errors, warnings
# cat /tmp/sql-profiling/analysis.txt

# 4. If evidence confirms stale statistics, propose a targeted update.
# Run it only after explicit authorization for this database.
sqlcmd -S myserver -d mydb -Q "UPDATE STATISTICS orders; UPDATE STATISTICS customers" --rw

# 5. Re-capture and compare
sqlcmd -S myserver -d mydb \
  -Q "SELECT o.*, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.date > '2024-01-01'" \
  --analyze-file /tmp/sql-profiling/analysis2.txt --summary
```
