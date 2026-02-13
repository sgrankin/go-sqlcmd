package plan

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// FormatText writes a human-readable analysis to the writer.
func FormatText(w io.Writer, result *Result) {
	for i, stmt := range result.Statements {
		if i > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, strings.Repeat("=", 60))
			fmt.Fprintln(w)
		}
		formatStatement(w, &stmt)
	}
}

func formatStatement(w io.Writer, stmt *StatementResult) {
	printSection(w, "StmtSimple", stmt.Attrs)

	if len(stmt.SetOptions) > 0 {
		printSection(w, "StatementSetOptions", stmt.SetOptions)
	}

	if len(stmt.PlanAttrs) > 0 {
		printSection(w, "QueryPlan", stmt.PlanAttrs)
	}

	if len(stmt.MemoryGrant) > 0 {
		printSection(w, "MemoryGrantInfo", stmt.MemoryGrant)
	}

	if len(stmt.HardwareProps) > 0 {
		printSection(w, "OptimizerHardwareDependentProperties", stmt.HardwareProps)
	}

	formatPlanWarnings(w, stmt.Warnings)

	if len(stmt.TimeStats) > 0 {
		printSection(w, "QueryTimeStats", stmt.TimeStats)
	}

	fmt.Fprintf(w, "\n=== OptimizerStatsUsage: %d statistics referenced ===\n", stmt.StatsRefCount)

	if stmt.Root != nil {
		fmt.Fprintln(w, "\n=== Full operator tree ===")
		printOperator(w, stmt.Root, 0, stmt.HasActualInfo)
	}

	if stmt.HasActualInfo {
		formatCardErrors(w, stmt.CardErrors)
	} else if stmt.Root != nil {
		fmt.Fprintln(w, "\n=== Estimated plan only (no runtime info) ===")
	}

	formatOpWarnings(w, stmt.OpWarnings)
	formatMissingStats(w, stmt.MissingStats)
}

func printSection(w io.Writer, title string, attrs map[string]string) {
	fmt.Fprintf(w, "\n=== %s ===\n", title)
	keys := sortedKeys(attrs)
	for _, k := range keys {
		v := attrs[k]
		if k == "StatementText" {
			fmt.Fprintf(w, "  %s: <%d chars>\n", k, len(v))
		} else {
			fmt.Fprintf(w, "  %s: %s\n", k, v)
		}
	}
}

func printOperator(w io.Writer, op *Operator, depth int, hasActual bool) {
	indent := strings.Repeat("  ", depth)
	if hasActual {
		fmt.Fprintf(w, "%sNode %3d %4dms  est=%8.0f act=%8d  %s (%s)%s\n",
			indent, op.NodeID, op.ElapsedMs, op.EstRows, op.ActualRows,
			op.PhysicalOp, op.LogicalOp, fmtObj(op.ObjectInfo))
	} else {
		fmt.Fprintf(w, "%sNode %3d  est=%8.0f  %s (%s)%s\n",
			indent, op.NodeID, op.EstRows,
			op.PhysicalOp, op.LogicalOp, fmtObj(op.ObjectInfo))
	}
	for _, child := range op.Children {
		printOperator(w, child, depth+1, hasActual)
	}
}

func fmtObj(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

func formatPlanWarnings(w io.Writer, warnings []Warning) {
	if len(warnings) == 0 {
		fmt.Fprintln(w, "\n=== No plan-level warnings ===")
		return
	}
	fmt.Fprintln(w, "\n=== Plan Warnings ===")
	for _, warn := range warnings {
		fmt.Fprintf(w, "  %s: %s\n", warn.Tag, formatAttrs(warn.Attrs))
		for _, sub := range warn.SubWarning {
			fmt.Fprintf(w, "    %s: %s\n", sub.Tag, formatAttrs(sub.Attrs))
		}
	}
}

func formatCardErrors(w io.Writer, errs []CardinalityError) {
	fmt.Fprintln(w, "\n=== Cardinality estimation errors ===")
	fmt.Fprintf(w, "%5s %10s %10s %8s %-25s %s\n",
		"Node", "EstRows", "ActRows", "Ratio", "Physical", "Logical")
	for _, e := range errs {
		fmt.Fprintf(w, "%5d %10.0f %10d %7.0fx %-25s %s (%s)\n",
			e.NodeID, e.EstRows, e.ActualRows, e.Ratio,
			e.PhysicalOp, e.LogicalOp, e.Direction)
	}
}

func formatOpWarnings(w io.Writer, warnings []OpWarning) {
	fmt.Fprintln(w, "\n=== Operator-level warnings ===")
	if len(warnings) == 0 {
		fmt.Fprintln(w, "  None")
		return
	}
	for _, warn := range warnings {
		if warn.Detail != "" {
			fmt.Fprintf(w, "  Node %d: %s: %s\n", warn.NodeID, warn.Tag, warn.Detail)
		} else {
			fmt.Fprintf(w, "  Node %d: %s\n", warn.NodeID, warn.Tag)
		}
	}
}

func formatMissingStats(w io.Writer, stats []map[string]string) {
	fmt.Fprintln(w, "\n=== Missing statistics ===")
	if len(stats) == 0 {
		fmt.Fprintln(w, "  None")
		return
	}
	for _, s := range stats {
		fmt.Fprintf(w, "  %s\n", formatAttrs(s))
	}
}

func formatAttrs(attrs map[string]string) string {
	keys := sortedKeys(attrs)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, attrs[k]))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
