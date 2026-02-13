package plan

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Analyze builds an analysis Result from parsed ShowPlanXML documents.
func Analyze(plans []*ShowPlanXML) *Result {
	var result Result
	for _, p := range plans {
		for _, stmt := range p.Statements {
			sr := analyzeStatement(&stmt)
			result.Statements = append(result.Statements, sr)
		}
	}
	return &result
}

func analyzeStatement(stmt *StmtSimple) StatementResult {
	sr := StatementResult{
		Attrs:      stmt.Attrs,
		SetOptions: stmt.SetOptions,
	}

	if stmt.QueryPlan == nil {
		return sr
	}
	qp := stmt.QueryPlan
	sr.PlanAttrs = qp.Attrs
	sr.MemoryGrant = qp.MemoryGrant
	sr.HardwareProps = qp.HardwareProps
	sr.TimeStats = qp.TimeStats
	sr.StatsRefCount = qp.StatsRefCount
	sr.Warnings = convertWarnings(qp.Warnings)

	if qp.Root != nil {
		sr.Root = convertOperator(qp.Root)
		sr.HasActualInfo = hasActualInfo(sr.Root)
		if sr.HasActualInfo {
			sr.CardErrors = computeCardinalityErrors(sr.Root)
		}
		sr.OpWarnings = collectOpWarnings(sr.Root)
		sr.MissingStats = collectMissingStats(qp.Root)
	}

	return sr
}

func convertOperator(relop *RelOp) *Operator {
	op := &Operator{
		NodeID:     relop.NodeID,
		PhysicalOp: relop.PhysicalOp,
		LogicalOp:  relop.LogicalOp,
		EstRows:    relop.EstRows,
		ObjectInfo: relop.ObjectInfo,
		Warnings:   convertWarnings(relop.Warnings),
	}

	// Aggregate across threads: SUM ActualRows, MAX ElapsedMs
	for _, t := range relop.Threads {
		op.ActualRows += t.ActualRows
		if t.ActualElapsed > op.ElapsedMs {
			op.ElapsedMs = t.ActualElapsed
		}
	}

	for _, child := range relop.Children {
		op.Children = append(op.Children, convertOperator(child))
	}
	return op
}

func convertWarnings(ws []xmlWarning) []Warning {
	var out []Warning
	for _, w := range ws {
		warning := Warning{
			Tag:   w.Tag,
			Attrs: w.Attrs,
		}
		for _, c := range w.Children {
			warning.SubWarning = append(warning.SubWarning, Warning{
				Tag:   c.Tag,
				Attrs: c.Attrs,
			})
		}
		out = append(out, warning)
	}
	return out
}

func hasActualInfo(op *Operator) bool {
	if op.ActualRows > 0 || op.ElapsedMs > 0 {
		return true
	}
	for _, child := range op.Children {
		if hasActualInfo(child) {
			return true
		}
	}
	return false
}

func computeCardinalityErrors(op *Operator) []CardinalityError {
	var errs []CardinalityError
	collectCardErrors(op, &errs)

	sort.Slice(errs, func(i, j int) bool {
		return errs[i].Ratio > errs[j].Ratio
	})

	// Return top 15
	if len(errs) > 15 {
		errs = errs[:15]
	}
	return errs
}

func collectCardErrors(op *Operator, errs *[]CardinalityError) {
	if op.EstRows > 0 && op.ActualRows > 0 {
		ratio := math.Max(
			float64(op.ActualRows)/op.EstRows,
			op.EstRows/float64(op.ActualRows),
		)
		direction := "under"
		if op.EstRows > float64(op.ActualRows) {
			direction = "over"
		}
		*errs = append(*errs, CardinalityError{
			NodeID:     op.NodeID,
			EstRows:    op.EstRows,
			ActualRows: op.ActualRows,
			Ratio:      ratio,
			PhysicalOp: op.PhysicalOp,
			LogicalOp:  op.LogicalOp,
			Direction:  direction,
			ObjectInfo: op.ObjectInfo,
		})
	}
	for _, child := range op.Children {
		collectCardErrors(child, errs)
	}
}

func collectOpWarnings(op *Operator) []OpWarning {
	var out []OpWarning
	walkOpWarnings(op, &out)
	return out
}

func walkOpWarnings(op *Operator, out *[]OpWarning) {
	for _, w := range op.Warnings {
		ow := OpWarning{
			NodeID: op.NodeID,
			Tag:    w.Tag,
			Attrs:  w.Attrs,
		}
		if w.Tag == "ColumnsWithNoStatistics" {
			var cols []string
			for _, sub := range w.SubWarning {
				cols = append(cols, fmt.Sprintf("%s.%s", stripBrackets(sub.Attrs["Table"]), stripBrackets(sub.Attrs["Column"])))
			}
			ow.Detail = strings.Join(cols, ", ")
		} else {
			var parts []string
			for k, v := range w.Attrs {
				parts = append(parts, fmt.Sprintf("%s=%s", k, v))
			}
			sort.Strings(parts)
			ow.Detail = strings.Join(parts, ", ")
		}
		*out = append(*out, ow)
	}
	for _, child := range op.Children {
		walkOpWarnings(child, out)
	}
}

func collectMissingStats(relop *RelOp) []map[string]string {
	var out []map[string]string
	walkMissingStats(relop, &out)
	return out
}

func walkMissingStats(relop *RelOp, out *[]map[string]string) {
	for _, w := range relop.Warnings {
		if w.Tag == "ColumnsWithNoStatistics" {
			for _, sub := range w.Children {
				*out = append(*out, sub.Attrs)
			}
		}
	}
	for _, child := range relop.Children {
		walkMissingStats(child, out)
	}
}
