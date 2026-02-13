package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func TestParseSimpleEstimated(t *testing.T) {
	data := readTestdata(t, "simple_estimated.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	p := plans[0]
	require.Len(t, p.Statements, 1)

	stmt := p.Statements[0]
	assert.Equal(t, "SELECT", stmt.Attrs["StatementType"])
	assert.Equal(t, "0.003", stmt.Attrs["StatementSubTreeCost"])
	assert.Equal(t, "true", stmt.SetOptions["ANSI_NULLS"])

	require.NotNil(t, stmt.QueryPlan)
	assert.Equal(t, "1", stmt.QueryPlan.Attrs["DegreeOfParallelism"])
	assert.Equal(t, "0", stmt.QueryPlan.MemoryGrant["SerialRequiredMemory"])

	require.NotNil(t, stmt.QueryPlan.Root)
	root := stmt.QueryPlan.Root
	assert.Equal(t, 0, root.NodeID)
	assert.Equal(t, "Clustered Index Scan", root.PhysicalOp)
	assert.Equal(t, 5.0, root.EstRows)
	assert.Equal(t, "[databases].[PK_databases]", root.ObjectInfo) // preserves brackets from XML attrs
	assert.Empty(t, root.Children)
	assert.Empty(t, root.Threads) // estimated only, no runtime info
}

func TestParseActualPlan(t *testing.T) {
	data := readTestdata(t, "actual_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	stmt := plans[0].Statements[0]
	require.NotNil(t, stmt.QueryPlan)
	assert.Equal(t, 2, stmt.QueryPlan.StatsRefCount)

	root := stmt.QueryPlan.Root
	assert.Equal(t, "Nested Loops", root.PhysicalOp)
	require.Len(t, root.Threads, 1)
	assert.Equal(t, int64(8), root.Threads[0].ActualRows)

	require.Len(t, root.Children, 2)
	assert.Equal(t, "Index Seek", root.Children[0].PhysicalOp)
	assert.Equal(t, "[orders].[IX_customer_id]", root.Children[0].ObjectInfo)
	assert.Equal(t, "Clustered Index Seek", root.Children[1].PhysicalOp)
}

func TestParseParallelPlan(t *testing.T) {
	data := readTestdata(t, "parallel_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	stmt := plans[0].Statements[0]
	assert.Equal(t, "4", stmt.QueryPlan.Attrs["DegreeOfParallelism"])

	// Walk to the Index Scan node (NodeId=3) — it has 4 threads
	indexScan := findRelOp(stmt.QueryPlan.Root, 3)
	require.NotNil(t, indexScan)
	assert.Len(t, indexScan.Threads, 4)
}

func TestParseWarnings(t *testing.T) {
	data := readTestdata(t, "warnings.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	stmt := plans[0].Statements[0]

	// Plan-level warnings
	require.Len(t, stmt.QueryPlan.Warnings, 1)
	assert.Equal(t, "PlanAffectingConvert", stmt.QueryPlan.Warnings[0].Tag)

	// Operator-level warnings on NodeId=1 (Table Scan)
	node1 := findRelOp(stmt.QueryPlan.Root, 1)
	require.NotNil(t, node1)
	require.Len(t, node1.Warnings, 1)
	assert.Equal(t, "ColumnsWithNoStatistics", node1.Warnings[0].Tag)
}

func TestAnalyzeEstimatedPlan(t *testing.T) {
	data := readTestdata(t, "simple_estimated.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	result := Analyze(plans)
	require.Len(t, result.Statements, 1)

	stmt := result.Statements[0]
	assert.False(t, stmt.HasActualInfo)
	assert.NotNil(t, stmt.Root)
	assert.Equal(t, "Clustered Index Scan", stmt.Root.PhysicalOp)
	assert.Empty(t, stmt.CardErrors)
}

func TestAnalyzeActualPlan(t *testing.T) {
	data := readTestdata(t, "actual_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	result := Analyze(plans)
	stmt := result.Statements[0]
	assert.True(t, stmt.HasActualInfo)
	assert.Equal(t, int64(8), stmt.Root.ActualRows)
	assert.Equal(t, int64(5), stmt.Root.ElapsedMs)

	// Cardinality errors should exist for nodes with est != actual
	assert.NotEmpty(t, stmt.CardErrors)
}

func TestAnalyzeParallelAggregation(t *testing.T) {
	data := readTestdata(t, "parallel_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	result := Analyze(plans)
	stmt := result.Statements[0]

	// Find the Index Scan operator (NodeId=3) - should SUM rows across 4 threads
	var indexScan *Operator
	var walk func(op *Operator)
	walk = func(op *Operator) {
		if op.NodeID == 3 {
			indexScan = op
			return
		}
		for _, c := range op.Children {
			walk(c)
		}
	}
	walk(stmt.Root)
	require.NotNil(t, indexScan)

	assert.Equal(t, int64(1000000), indexScan.ActualRows) // SUM of 250k * 4
	assert.Equal(t, int64(52), indexScan.ElapsedMs)       // MAX of 50,48,52,49
}

func TestAnalyzeWarnings(t *testing.T) {
	data := readTestdata(t, "warnings.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	result := Analyze(plans)
	stmt := result.Statements[0]

	// Plan-level warnings
	require.Len(t, stmt.Warnings, 1)
	assert.Equal(t, "PlanAffectingConvert", stmt.Warnings[0].Tag)

	// Operator warnings
	require.NotEmpty(t, stmt.OpWarnings)
	found := false
	for _, w := range stmt.OpWarnings {
		if w.Tag == "ColumnsWithNoStatistics" {
			found = true
			assert.Equal(t, 1, w.NodeID)
			assert.Contains(t, w.Detail, "[t1].val")
		}
	}
	assert.True(t, found, "expected ColumnsWithNoStatistics warning")

	// Missing stats
	require.NotEmpty(t, stmt.MissingStats)
}

func TestAnalyzeCardinalityErrors(t *testing.T) {
	data := readTestdata(t, "warnings.xml")
	plans, err := Parse(data)
	require.NoError(t, err)

	result := Analyze(plans)
	stmt := result.Statements[0]

	// NodeId=0: est=50000, act=1200 → ratio ~41.7, direction=over
	require.NotEmpty(t, stmt.CardErrors)
	topErr := stmt.CardErrors[0]
	assert.Equal(t, 0, topErr.NodeID)
	assert.Equal(t, "over", topErr.Direction)
	assert.InDelta(t, 41.67, topErr.Ratio, 0.5)
}

func TestFormatText(t *testing.T) {
	data := readTestdata(t, "actual_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "=== StmtSimple ===")
	assert.Contains(t, output, "=== QueryPlan ===")
	assert.Contains(t, output, "=== Full operator tree ===")
	assert.Contains(t, output, "Nested Loops")
	assert.Contains(t, output, "Index Seek")
	assert.Contains(t, output, "=== Cardinality estimation errors ===")
}

func TestFormatTextEstimated(t *testing.T) {
	data := readTestdata(t, "simple_estimated.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Estimated plan only")
	assert.NotContains(t, output, "Cardinality estimation errors")
}

func TestFormatJSON(t *testing.T) {
	data := readTestdata(t, "actual_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	err = FormatJSON(&buf, result)
	require.NoError(t, err)

	// Verify it's valid JSON
	var decoded map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &decoded)
	require.NoError(t, err)
	assert.Contains(t, decoded, "Statements")
}

func TestCSVWrapped(t *testing.T) {
	// Simulate CSV-wrapped XML (single quoted blob)
	xmlData := readTestdata(t, "simple_estimated.xml")
	csvWrapped := `"` + strings.ReplaceAll(string(xmlData), `"`, `""`) + `"`

	plans, err := Parse([]byte(csvWrapped))
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, "SELECT", plans[0].Statements[0].Attrs["StatementType"])
}

func TestMultiplePlanDocuments(t *testing.T) {
	data1 := readTestdata(t, "simple_estimated.xml")
	data2 := readTestdata(t, "actual_plan.xml")
	combined := string(data1) + "\n" + string(data2)

	plans, err := Parse([]byte(combined))
	require.NoError(t, err)
	require.Len(t, plans, 2)
}

// findRelOp walks the RelOp tree to find a node by ID.
func findRelOp(relop *RelOp, nodeID int) *RelOp {
	if relop == nil {
		return nil
	}
	if relop.NodeID == nodeID {
		return relop
	}
	for _, child := range relop.Children {
		if found := findRelOp(child, nodeID); found != nil {
			return found
		}
	}
	return nil
}
