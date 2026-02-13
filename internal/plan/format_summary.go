package plan

import (
	"fmt"
	"io"
)

// FormatSummary writes a concise summary of the analysis to the writer.
// It reports cost, DOP, timing, top cardinality errors, warning counts,
// and missing statistics — all in a compact format suitable for AI tools.
func FormatSummary(w io.Writer, result *Result) {
	for i, stmt := range result.Statements {
		if i > 0 {
			fmt.Fprintln(w)
		}
		formatSummaryStatement(w, &stmt)
	}
}

func formatSummaryStatement(w io.Writer, stmt *StatementResult) {
	// Cost, DOP, timing line
	var metrics []string
	if v, ok := stmt.PlanAttrs["CachedPlanSize"]; ok {
		metrics = append(metrics, fmt.Sprintf("CachedPlanSize: %s", v))
	}
	if v, ok := stmt.PlanAttrs["CompileTime"]; ok {
		metrics = append(metrics, fmt.Sprintf("CompileTime: %sms", v))
	}
	if v, ok := stmt.Attrs["StatementSubTreeCost"]; ok {
		metrics = append(metrics, fmt.Sprintf("Cost: %s", v))
	}
	if v, ok := stmt.PlanAttrs["DegreeOfParallelism"]; ok {
		metrics = append(metrics, fmt.Sprintf("DOP: %s", v))
	}
	if elapsed, ok := stmt.TimeStats["ElapsedTime"]; ok {
		metrics = append(metrics, fmt.Sprintf("Elapsed: %sms", elapsed))
	}
	if cpu, ok := stmt.TimeStats["CpuTime"]; ok {
		metrics = append(metrics, fmt.Sprintf("CPU: %sms", cpu))
	}

	if len(metrics) > 0 {
		for i, m := range metrics {
			if i > 0 {
				fmt.Fprint(w, " | ")
			}
			fmt.Fprint(w, m)
		}
		fmt.Fprintln(w)
	}

	// Top 3 cardinality errors
	if len(stmt.CardErrors) > 0 {
		fmt.Fprint(w, "Cardinality errors:")
		limit := 3
		if len(stmt.CardErrors) < limit {
			limit = len(stmt.CardErrors)
		}
		for i := 0; i < limit; i++ {
			e := stmt.CardErrors[i]
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, " Node %d (%.0fx %s)", e.NodeID, e.Ratio, e.Direction)
		}
		if len(stmt.CardErrors) > 3 {
			fmt.Fprintf(w, " (+%d more)", len(stmt.CardErrors)-3)
		}
		fmt.Fprintln(w)
	}

	// Warning counts
	planWarnings := len(stmt.Warnings)
	opWarnings := len(stmt.OpWarnings)
	if planWarnings > 0 || opWarnings > 0 {
		fmt.Fprintf(w, "Warnings: %d plan-level, %d operator-level\n", planWarnings, opWarnings)
	}

	// Missing statistics
	if len(stmt.MissingStats) > 0 {
		fmt.Fprintf(w, "Missing statistics: %d\n", len(stmt.MissingStats))
	}
}
