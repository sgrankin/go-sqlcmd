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
	assert.Equal(t, "databases.PK_databases", root.ObjectInfo)
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
	assert.Equal(t, "orders.IX_customer_id", root.Children[0].ObjectInfo)
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
			assert.Contains(t, w.Detail, "t1.val")
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

	// Summary header
	assert.Contains(t, output, "Elapsed: 5ms")
	assert.Contains(t, output, "CPU: 3ms")
	assert.Contains(t, output, "DOP: 1")
	assert.Contains(t, output, "Cost: 0.05")
	assert.Contains(t, output, "Compile: 5ms")

	// Operator tree
	assert.Contains(t, output, "Operator Tree:")
	assert.Contains(t, output, "Nested Loops")
	assert.Contains(t, output, "Index Seek")
	assert.Contains(t, output, "orders.IX_customer_id")

	// Should have hot markers (★) on top elapsed nodes
	assert.Contains(t, output, "★")

	// No >10x cardinality errors in actual_plan.xml (all ratios are small)
	assert.NotContains(t, output, "Cardinality Errors")
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
	assert.Contains(t, output, "est=5")
	assert.Contains(t, output, "Clustered Index Scan")
	assert.NotContains(t, output, "Cardinality Errors")
}

func TestFormatTextWarnings(t *testing.T) {
	data := readTestdata(t, "warnings.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	// >10x cardinality error (50000 est vs 1200 act = ~41.7x)
	assert.Contains(t, output, "Cardinality Errors (>10x):")
	assert.Contains(t, output, "over")

	// Warnings section
	assert.Contains(t, output, "Warnings:")
	assert.Contains(t, output, "ColumnsWithNoStatistics")
	assert.Contains(t, output, "t1.val")

	// Missing statistics
	assert.Contains(t, output, "Missing Statistics:")
	assert.Contains(t, output, "t1.val")
}

func TestFormatTextHotNodes(t *testing.T) {
	// Build a result with varying elapsed times
	result := &Result{
		Statements: []StatementResult{{
			HasActualInfo: true,
			Root: &Operator{
				NodeID: 0, PhysicalOp: "Root", ElapsedMs: 100, ActualRows: 1,
				Children: []*Operator{
					{NodeID: 1, PhysicalOp: "Child1", ElapsedMs: 50, ActualRows: 10},
					{NodeID: 2, PhysicalOp: "Child2", ElapsedMs: 5, ActualRows: 10},
					{NodeID: 3, PhysicalOp: "Child3", ElapsedMs: 80, ActualRows: 10},
				},
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	// Children should be hot-marked, but root should not
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Root") {
			assert.NotContains(t, line, "★", "root node should not be hot-marked")
		}
	}
	assert.Contains(t, output, "★")
	assert.Contains(t, output, "Child1")
	assert.Contains(t, output, "Child3")
}

func TestFormatTextCardErrorFiltering(t *testing.T) {
	// Build a result with card errors at various ratios
	result := &Result{
		Statements: []StatementResult{{
			HasActualInfo: true,
			Root:          &Operator{NodeID: 0, PhysicalOp: "Root"},
			CardErrors: []CardinalityError{
				{NodeID: 1, EstRows: 100, ActualRows: 1, Ratio: 100, Direction: "over", PhysicalOp: "Scan", ObjectInfo: "t1.IX_a"},
				{NodeID: 2, EstRows: 10, ActualRows: 5, Ratio: 2, Direction: "over", PhysicalOp: "Seek"},
				{NodeID: 3, EstRows: 1, ActualRows: 50, Ratio: 50, Direction: "under", PhysicalOp: "Seek", ObjectInfo: "t2.PK"},
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	// >10x errors should appear
	assert.Contains(t, output, "Node   1")
	assert.Contains(t, output, "t1.IX_a")
	assert.Contains(t, output, "Node   3")
	assert.Contains(t, output, "t2.PK")

	// 2x error should NOT appear
	assert.NotContains(t, output, "Node   2")
}

func TestCardinalityErrorObjectInfo(t *testing.T) {
	data := readTestdata(t, "warnings.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	stmt := result.Statements[0]
	// Node 2 (Index Scan on t2.IX_id) should have ObjectInfo
	for _, e := range stmt.CardErrors {
		if e.NodeID == 2 {
			assert.Equal(t, "t2.IX_id", e.ObjectInfo)
		}
	}
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

func TestFormatTextMultiStatement(t *testing.T) {
	// Cover the multi-statement separator in FormatText
	result := &Result{
		Statements: []StatementResult{
			{
				Attrs:     map[string]string{"StatementSubTreeCost": "1.0"},
				PlanAttrs: map[string]string{"DegreeOfParallelism": "1"},
				Root:      &Operator{NodeID: 0, PhysicalOp: "Scan", EstRows: 10},
			},
			{
				Attrs:     map[string]string{"StatementSubTreeCost": "2.0"},
				PlanAttrs: map[string]string{"DegreeOfParallelism": "2"},
				Root:      &Operator{NodeID: 0, PhysicalOp: "Seek", EstRows: 20},
			},
		},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Cost: 1.0")
	assert.Contains(t, output, "Cost: 2.0")
	assert.Contains(t, output, "====") // separator between statements
	assert.Contains(t, output, "Scan")
	assert.Contains(t, output, "Seek")
}

func TestFormatTextGrantedMemory(t *testing.T) {
	// Cover the GrantedMemory + MaxUsedMemory branch in writeSummaryHeader
	result := &Result{
		Statements: []StatementResult{{
			Attrs: map[string]string{"StatementSubTreeCost": "5.0"},
			MemoryGrant: map[string]string{
				"GrantedMemory":  "208968",
				"MaxUsedMemory":  "50120",
				"DesiredMemory":  "300000",
				"RequestedMemory": "250000",
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Memory: 208968KB granted, 50120KB used")
}

func TestFormatTextGrantedMemoryNoUsed(t *testing.T) {
	// Cover the GrantedMemory without MaxUsedMemory branch
	result := &Result{
		Statements: []StatementResult{{
			MemoryGrant: map[string]string{
				"GrantedMemory": "1024",
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Memory: 1024KB granted")
	assert.NotContains(t, output, "used")
}

func TestFormatTextPlanHashAndEarlyAbort(t *testing.T) {
	// Cover QueryPlanHash and StatementOptmEarlyAbortReason
	result := &Result{
		Statements: []StatementResult{{
			Attrs: map[string]string{
				"QueryPlanHash":                "0x5E2464189556E65D",
				"StatementOptmLevel":           "FULL",
				"StatementOptmEarlyAbortReason": "TimeOut",
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "PlanHash: 0x5E2464189556E65D")
	assert.Contains(t, output, "Optimizer: FULL (TimeOut)")
}

func TestFormatTextWarningNoDetail(t *testing.T) {
	// Cover the warning with empty Detail (non-ColumnsWithNoStatistics)
	result := &Result{
		Statements: []StatementResult{{
			HasActualInfo: true,
			Root:          &Operator{NodeID: 0, PhysicalOp: "Root"},
			OpWarnings: []OpWarning{
				{NodeID: 5, Tag: "SpillToTempDb"},
				{NodeID: 3, Tag: "NoJoinPredicate", Detail: "some detail"},
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Node 5: SpillToTempDb")
	assert.NotContains(t, output, "SpillToTempDb —")
	assert.Contains(t, output, "Node 3: NoJoinPredicate — some detail")
}

func TestFormatTextMissingStatsFallback(t *testing.T) {
	// Cover the formatAttrs fallback in writeMissingStats
	// when Table/Column keys aren't present
	result := &Result{
		Statements: []StatementResult{{
			MissingStats: []map[string]string{
				{"Schema": "[dbo]", "Table": "[t1]", "Column": "val"},
				{"SomeKey": "SomeValue"},
			},
		}},
	}

	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Missing Statistics:")
	assert.Contains(t, output, "t1.val")
	assert.Contains(t, output, "SomeKey=SomeValue")
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
