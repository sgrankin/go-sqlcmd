// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: SQL Server only produces showplan XML for statements that go through the
// query optimizer (table access, joins, etc.). Trivial constant expressions like
// SELECT 1 are optimized away and do not produce a showplan result set.

func TestPlanFileOutputsShowPlanXML(t *testing.T) {
	s, _ := setupSqlCmdWithMemoryOutput(t)
	planBuf := &bytes.Buffer{}
	s.PlanFile = planBuf

	s.Query = "SELECT name FROM sys.databases WHERE database_id = 1"
	err := s.Run(true, false)
	require.NoError(t, err)

	plan := planBuf.String()
	assert.Contains(t, plan, "<ShowPlanXML")
	assert.Contains(t, plan, "</ShowPlanXML>")
}

func TestPlanFilePreservesDataOutput(t *testing.T) {
	s, dataBuf := setupSqlCmdWithMemoryOutput(t)
	planBuf := &bytes.Buffer{}
	s.PlanFile = planBuf

	s.Query = "SELECT name FROM sys.databases WHERE database_id = 1"
	err := s.Run(true, false)
	require.NoError(t, err)

	data := dataBuf.buf.String()
	assert.Contains(t, data, "master", "data result should still appear in normal output")

	plan := planBuf.String()
	assert.NotContains(t, plan, "master"+SqlcmdEol, "plain data should not appear in plan output")
	assert.Contains(t, plan, "<ShowPlanXML")
}

func TestPlanFileSeparatesXMLFromData(t *testing.T) {
	s, dataBuf := setupSqlCmdWithMemoryOutput(t)
	planBuf := &bytes.Buffer{}
	s.PlanFile = planBuf

	s.Query = "SELECT name FROM sys.databases WHERE database_id = 1"
	err := s.Run(true, false)
	require.NoError(t, err)

	data := dataBuf.buf.String()
	plan := planBuf.String()

	// Data output should have the database name, not XML
	assert.Contains(t, data, "master")
	assert.NotContains(t, data, "<ShowPlanXML")

	// Plan output should have XML, not the database name as plain text
	assert.Contains(t, plan, "<ShowPlanXML")
}

func TestPlanFileMultipleBatches(t *testing.T) {
	s, _ := setupSqlCmdWithMemoryOutput(t)
	planBuf := &bytes.Buffer{}
	s.PlanFile = planBuf

	// Two separate queries that access tables (to ensure showplan output)
	err := runSqlCmd(t, s, []string{
		"SELECT name FROM sys.databases WHERE database_id = 1",
		"GO",
		"SELECT name FROM sys.databases WHERE database_id = 2",
		"GO",
	})
	assert.NoError(t, err)

	plan := planBuf.String()
	count := strings.Count(plan, "<ShowPlanXML")
	assert.Equal(t, 2, count, "expected two showplan XML elements for two batches")
}

func TestPlanFileNilDoesNotAddStatisticsXML(t *testing.T) {
	// When PlanFile is nil, runQuery should not enable statistics XML.
	s, dataBuf := setupSqlCmdWithMemoryOutput(t)
	s.PlanFile = nil

	s.Query = "SELECT name FROM sys.databases WHERE database_id = 1"
	err := s.Run(true, false)
	require.NoError(t, err)

	data := dataBuf.buf.String()
	assert.NotContains(t, data, "<ShowPlanXML", "without PlanFile, no plan XML should appear")
}

func TestPlanFileTrivialQueryNoShowplan(t *testing.T) {
	// SQL Server does not produce showplan for trivial constant expressions.
	// Verify that --plan-file does not break such queries.
	s, dataBuf := setupSqlCmdWithMemoryOutput(t)
	planBuf := &bytes.Buffer{}
	s.PlanFile = planBuf

	s.Query = "SELECT 1 AS x"
	err := s.Run(true, false)
	require.NoError(t, err)

	data := dataBuf.buf.String()
	assert.Contains(t, data, "1", "trivial query should still produce data output")
	// No showplan for trivial queries — plan buffer may be empty
	assert.Equal(t, "", planBuf.String(), "no showplan for constant expression")
}

func TestEstimatedPlanDoesNotExecuteQuery(t *testing.T) {
	s, _ := setupSqlCmdWithMemoryOutput(t)
	_, err := s.db.ExecContext(t.Context(), "CREATE TABLE #estimated_plan_test (id INT)")
	require.NoError(t, err)

	planBuf := &bytes.Buffer{}
	s.PlanFile = planBuf
	s.EstimatedPlan = true
	s.Query = "INSERT INTO #estimated_plan_test VALUES (1)"

	require.NoError(t, s.Run(true, false))
	assert.Contains(t, planBuf.String(), "<ShowPlanXML")
	assert.NotContains(t, planBuf.String(), "<RunTimeInformation>")

	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM #estimated_plan_test").Scan(&count))
	assert.Zero(t, count, "estimated plan query must not execute")
}
