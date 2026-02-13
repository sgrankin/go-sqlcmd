package plan

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// FormatText writes a compact, scannable analysis to the writer.
// The format is optimized for linear reading: find where time goes and why.
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
	// Summary header
	writeSummaryHeader(w, stmt)

	// Operator tree
	if stmt.Root != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Operator Tree:")
		hotSet := computeHotSet(stmt.Root, 5)
		writeOperatorTree(w, stmt.Root, 0, stmt.HasActualInfo, hotSet)
	}

	// Cardinality errors (>10x only)
	if stmt.HasActualInfo {
		writeCardErrors(w, stmt.CardErrors)
	} else if stmt.Root != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(Estimated plan only — no runtime info)")
	}

	// Warnings
	writeWarnings(w, stmt.OpWarnings)

	// Missing statistics
	writeMissingStats(w, stmt.MissingStats)
}

func writeSummaryHeader(w io.Writer, stmt *StatementResult) {
	// Line 1: Elapsed, CPU, DOP, Cost
	var line1 []string
	if v, ok := stmt.TimeStats["ElapsedTime"]; ok {
		line1 = append(line1, fmt.Sprintf("Elapsed: %sms", v))
	}
	if v, ok := stmt.TimeStats["CpuTime"]; ok {
		line1 = append(line1, fmt.Sprintf("CPU: %sms", v))
	}
	if v, ok := stmt.PlanAttrs["DegreeOfParallelism"]; ok {
		line1 = append(line1, fmt.Sprintf("DOP: %s", v))
	}
	if v, ok := stmt.Attrs["StatementSubTreeCost"]; ok {
		line1 = append(line1, fmt.Sprintf("Cost: %s", v))
	}
	if len(line1) > 0 {
		fmt.Fprintln(w, strings.Join(line1, " | "))
	}

	// Line 2: Compile info, memory grant
	var line2 []string
	if v, ok := stmt.PlanAttrs["CompileTime"]; ok {
		line2 = append(line2, fmt.Sprintf("Compile: %sms", v))
	}
	if granted, ok := stmt.MemoryGrant["GrantedMemory"]; ok {
		if used, ok2 := stmt.MemoryGrant["MaxUsedMemory"]; ok2 {
			line2 = append(line2, fmt.Sprintf("Memory: %sKB granted, %sKB used", granted, used))
		} else {
			line2 = append(line2, fmt.Sprintf("Memory: %sKB granted", granted))
		}
	} else {
		// Fall back to serial memory info
		if req, ok := stmt.MemoryGrant["SerialRequiredMemory"]; ok {
			if des, ok2 := stmt.MemoryGrant["SerialDesiredMemory"]; ok2 {
				line2 = append(line2, fmt.Sprintf("Memory: %sKB required, %sKB desired", req, des))
			}
		}
	}
	if len(line2) > 0 {
		fmt.Fprintln(w, strings.Join(line2, " | "))
	}

	// Line 3: PlanHash, Optimizer level
	var line3 []string
	if v, ok := stmt.Attrs["QueryPlanHash"]; ok {
		line3 = append(line3, fmt.Sprintf("PlanHash: %s", v))
	}
	if v, ok := stmt.Attrs["StatementOptmLevel"]; ok {
		optInfo := v
		if timeout, ok2 := stmt.Attrs["StatementOptmEarlyAbortReason"]; ok2 {
			optInfo += " (" + timeout + ")"
		}
		line3 = append(line3, fmt.Sprintf("Optimizer: %s", optInfo))
	}
	if len(line3) > 0 {
		fmt.Fprintln(w, strings.Join(line3, " | "))
	}
}

// computeHotSet returns the NodeIDs of the top-N operators by exclusive elapsed time.
// Exclusive elapsed = node's elapsed minus its slowest child's elapsed, which isolates
// the work done by the operator itself rather than its subtree.
// The root node is excluded since its elapsed is always ~= total and isn't actionable.
func computeHotSet(root *Operator, n int) map[int]bool {
	type nodeElapsed struct {
		id      int
		elapsed int64
	}
	var nodes []nodeElapsed
	var walk func(op *Operator, isRoot bool)
	walk = func(op *Operator, isRoot bool) {
		if !isRoot {
			exclusive := exclusiveElapsed(op)
			if exclusive > 0 {
				nodes = append(nodes, nodeElapsed{op.NodeID, exclusive})
			}
		}
		for _, c := range op.Children {
			walk(c, false)
		}
	}
	walk(root, true)

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].elapsed > nodes[j].elapsed
	})

	hot := make(map[int]bool)
	for i := 0; i < n && i < len(nodes); i++ {
		hot[nodes[i].id] = true
	}
	return hot
}

// exclusiveElapsed returns the elapsed time attributable to this operator alone,
// excluding time spent in child operators.
func exclusiveElapsed(op *Operator) int64 {
	var maxChild int64
	for _, c := range op.Children {
		if c.ElapsedMs > maxChild {
			maxChild = c.ElapsedMs
		}
	}
	if op.ElapsedMs > maxChild {
		return op.ElapsedMs - maxChild
	}
	return 0
}

func writeOperatorTree(w io.Writer, op *Operator, depth int, hasActual bool, hotSet map[int]bool) {
	indent := strings.Repeat("  ", depth)
	isLeaf := len(op.Children) == 0
	obj := ""
	if isLeaf && op.ObjectInfo != "" {
		obj = " " + op.ObjectInfo
	}

	if hasActual {
		hot := ""
		if hotSet[op.NodeID] {
			hot = "  \u2605"
		}
		fmt.Fprintf(w, "[%3d] %s%-30s %8dr (est %6.0f) %5dms%s%s\n",
			op.NodeID, indent, op.PhysicalOp,
			op.ActualRows, op.EstRows, op.ElapsedMs, obj, hot)
	} else {
		fmt.Fprintf(w, "[%3d] %s%-30s est=%.0f%s\n",
			op.NodeID, indent, op.PhysicalOp,
			op.EstRows, obj)
	}

	for _, child := range op.Children {
		writeOperatorTree(w, child, depth+1, hasActual, hotSet)
	}
}

func writeCardErrors(w io.Writer, errs []CardinalityError) {
	// Filter to >10x only
	var significant []CardinalityError
	for _, e := range errs {
		if e.Ratio > 10 {
			significant = append(significant, e)
		}
	}
	if len(significant) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cardinality Errors (>10x):")
	for _, e := range significant {
		obj := ""
		if e.ObjectInfo != "" {
			obj = "  " + e.ObjectInfo
		}
		fmt.Fprintf(w, "  Node %3d: %8.0f est \u2192 %8d act (%5.0fx %s)  %s%s\n",
			e.NodeID, e.EstRows, e.ActualRows, e.Ratio, e.Direction,
			e.PhysicalOp, obj)
	}
}

func writeWarnings(w io.Writer, warnings []OpWarning) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Warnings:")
	for _, warn := range warnings {
		if warn.Detail != "" {
			fmt.Fprintf(w, "  Node %d: %s \u2014 %s\n", warn.NodeID, warn.Tag, warn.Detail)
		} else {
			fmt.Fprintf(w, "  Node %d: %s\n", warn.NodeID, warn.Tag)
		}
	}
}

func writeMissingStats(w io.Writer, stats []map[string]string) {
	if len(stats) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Missing Statistics:")
	for _, s := range stats {
		table := stripBrackets(s["Table"])
		col := stripBrackets(s["Column"])
		if table != "" && col != "" {
			fmt.Fprintf(w, "  %s.%s\n", table, col)
		} else {
			fmt.Fprintf(w, "  %s\n", formatAttrs(s))
		}
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
