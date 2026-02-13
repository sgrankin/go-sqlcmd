// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupJSONLCmd creates a Sqlcmd with JSONL formatter and separate out/err buffers.
func setupJSONLCmd(t *testing.T) (*Sqlcmd, *memoryBuffer, *memoryBuffer) {
	t.Helper()
	s, outBuf := setupSqlCmdWithMemoryOutput(t)
	s.Format = NewJSONLFormatter()
	errBuf := &memoryBuffer{buf: new(bytes.Buffer)}
	s.SetError(errBuf)
	return s, outBuf, errBuf
}

func TestJSONLBasicQuery(t *testing.T) {
	s, outBuf, _ := setupJSONLCmd(t)

	s.Query = "SELECT 1 AS id, 'hello' AS name"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := strings.TrimSpace(outBuf.buf.String())
	lines := strings.Split(output, "\n")
	require.Len(t, lines, 1, "should have exactly 1 line")

	var row map[string]interface{}
	err = json.Unmarshal([]byte(lines[0]), &row)
	require.NoError(t, err)
	assert.Equal(t, float64(1), row["id"], "integer should be a JSON number")
	assert.Equal(t, "hello", row["name"])
}

func TestJSONLNullHandling(t *testing.T) {
	s, outBuf, _ := setupJSONLCmd(t)

	s.Query = "SELECT NULL AS val"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := strings.TrimSpace(outBuf.buf.String())
	var row map[string]interface{}
	err = json.Unmarshal([]byte(output), &row)
	require.NoError(t, err)
	assert.Nil(t, row["val"], "NULL should be JSON null")
}

func TestJSONLTypeFidelity(t *testing.T) {
	s, outBuf, _ := setupJSONLCmd(t)

	s.Query = `SELECT
		CAST(42 AS INT) AS int_val,
		CAST(3.14 AS FLOAT) AS float_val,
		CAST(1 AS BIT) AS true_val,
		CAST(0 AS BIT) AS false_val,
		'text' AS str_val,
		NULL AS null_val`
	err := s.Run(true, false)
	require.NoError(t, err)

	output := strings.TrimSpace(outBuf.buf.String())
	var row map[string]interface{}
	err = json.Unmarshal([]byte(output), &row)
	require.NoError(t, err)

	assert.Equal(t, float64(42), row["int_val"], "int should be a number")
	assert.Equal(t, 3.14, row["float_val"], "float should be a number")
	assert.Equal(t, true, row["true_val"], "BIT 1 should be true")
	assert.Equal(t, false, row["false_val"], "BIT 0 should be false")
	assert.Equal(t, "text", row["str_val"])
	assert.Nil(t, row["null_val"])
}

func TestJSONLMultipleRows(t *testing.T) {
	s, outBuf, _ := setupJSONLCmd(t)

	s.Query = "SELECT name, database_id FROM sys.databases WHERE database_id <= 4 ORDER BY database_id"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := strings.TrimSpace(outBuf.buf.String())
	lines := strings.Split(output, "\n")
	require.GreaterOrEqual(t, len(lines), 1, "should have at least 1 line")

	var firstRow map[string]interface{}
	err = json.Unmarshal([]byte(lines[0]), &firstRow)
	require.NoError(t, err)
	assert.Equal(t, "master", firstRow["name"])
	assert.Equal(t, float64(1), firstRow["database_id"], "database_id should be a number")
}

func TestJSONLMultiResultSet(t *testing.T) {
	s, outBuf, _ := setupJSONLCmd(t)

	s.Query = "SELECT 1 AS a; SELECT 'x' AS b"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := strings.TrimSpace(outBuf.buf.String())
	lines := strings.Split(output, "\n")
	require.Len(t, lines, 2, "should have 2 lines (one per result set)")

	var row1 map[string]interface{}
	err = json.Unmarshal([]byte(lines[0]), &row1)
	require.NoError(t, err)
	assert.Equal(t, float64(1), row1["a"])

	var row2 map[string]interface{}
	err = json.Unmarshal([]byte(lines[1]), &row2)
	require.NoError(t, err)
	assert.Equal(t, "x", row2["b"])
}

func TestJSONLMessagesToStderr(t *testing.T) {
	s, outBuf, errBuf := setupJSONLCmd(t)

	s.Query = "PRINT 'hello from print'; SELECT 1 AS val"
	err := s.Run(true, false)
	require.NoError(t, err)

	// stdout should only have JSONL data
	output := strings.TrimSpace(outBuf.buf.String())
	var row map[string]interface{}
	err = json.Unmarshal([]byte(output), &row)
	require.NoError(t, err)
	assert.Equal(t, float64(1), row["val"])

	// stderr should have the PRINT message
	assert.Contains(t, errBuf.buf.String(), "hello from print")
}

func TestJSONLDateTypes(t *testing.T) {
	s, outBuf, _ := setupJSONLCmd(t)

	s.Query = `SELECT
		CAST('2024-01-15' AS DATE) AS date_val,
		CAST('2024-01-15 10:30:00' AS DATETIME) AS datetime_val`
	err := s.Run(true, false)
	require.NoError(t, err)

	output := strings.TrimSpace(outBuf.buf.String())
	var row map[string]interface{}
	err = json.Unmarshal([]byte(output), &row)
	require.NoError(t, err)

	assert.Equal(t, "2024-01-15", row["date_val"])
	assert.Contains(t, row["datetime_val"].(string), "2024-01-15")
}
