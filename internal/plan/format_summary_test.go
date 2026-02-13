package plan

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSummaryActualPlan(t *testing.T) {
	data := readTestdata(t, "actual_plan.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	FormatSummary(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Cost:")
	assert.Contains(t, output, "DOP:")
	assert.Contains(t, output, "Cardinality errors:")
}

func TestFormatSummaryEstimatedPlan(t *testing.T) {
	data := readTestdata(t, "simple_estimated.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	FormatSummary(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Cost:")
	assert.Contains(t, output, "DOP:")
	// No cardinality errors for estimated plans
	assert.NotContains(t, output, "Cardinality errors:")
}

func TestFormatSummaryWarnings(t *testing.T) {
	data := readTestdata(t, "warnings.xml")
	plans, err := Parse(data)
	require.NoError(t, err)
	result := Analyze(plans)

	var buf bytes.Buffer
	FormatSummary(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Warnings:")
	assert.Contains(t, output, "Missing statistics:")
	assert.Contains(t, output, "Cardinality errors:")
}

func TestFormatSummaryTopThreeCardErrors(t *testing.T) {
	// Build a result with 5 cardinality errors
	result := &Result{
		Statements: []StatementResult{{
			Attrs:     map[string]string{"StatementSubTreeCost": "10"},
			PlanAttrs: map[string]string{"DegreeOfParallelism": "1"},
			CardErrors: []CardinalityError{
				{NodeID: 1, Ratio: 100, Direction: "over"},
				{NodeID: 2, Ratio: 50, Direction: "under"},
				{NodeID: 3, Ratio: 25, Direction: "over"},
				{NodeID: 4, Ratio: 10, Direction: "under"},
				{NodeID: 5, Ratio: 5, Direction: "over"},
			},
		}},
	}

	var buf bytes.Buffer
	FormatSummary(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "Node 1 (100x over)")
	assert.Contains(t, output, "Node 2 (50x under)")
	assert.Contains(t, output, "Node 3 (25x over)")
	assert.NotContains(t, output, "Node 4")
	assert.Contains(t, output, "(+2 more)")
}

func TestFormatSummaryEmpty(t *testing.T) {
	result := &Result{
		Statements: []StatementResult{{
			Attrs: map[string]string{},
		}},
	}

	var buf bytes.Buffer
	FormatSummary(&buf, result)
	output := buf.String()

	// Should not contain any sections
	assert.NotContains(t, output, "Cardinality errors:")
	assert.NotContains(t, output, "Warnings:")
	assert.NotContains(t, output, "Missing statistics:")
}
