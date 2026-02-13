// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupCSVCmd creates a Sqlcmd with CSV formatter and separate out/err buffers.
func setupCSVCmd(t *testing.T) (*Sqlcmd, *memoryBuffer, *memoryBuffer) {
	t.Helper()
	s, outBuf := setupSqlCmdWithMemoryOutput(t)
	s.Format = NewCSVFormatter()
	errBuf := &memoryBuffer{buf: new(bytes.Buffer)}
	s.SetError(errBuf)
	return s, outBuf, errBuf
}

func TestCSVBasicQuery(t *testing.T) {
	s, outBuf, _ := setupCSVCmd(t)

	s.Query = "SELECT 1 AS id, 'hello' AS name"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := outBuf.buf.String()
	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2, "should have header + 1 data row")
	assert.Equal(t, []string{"id", "name"}, records[0])
	assert.Equal(t, []string{"1", "hello"}, records[1])
}

func TestCSVNullHandling(t *testing.T) {
	s, outBuf, _ := setupCSVCmd(t)

	// Use a multi-column query so the NULL row produces a visible CSV record
	s.Query = "SELECT NULL AS val, 'x' AS marker"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := outBuf.buf.String()
	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "", records[1][0], "NULL should be empty string in CSV")
	assert.Equal(t, "x", records[1][1])
}

func TestCSVSpecialCharacters(t *testing.T) {
	s, outBuf, _ := setupCSVCmd(t)

	s.Query = `SELECT 'hello,world' AS comma_val, 'say "hi"' AS quote_val, 'line1
line2' AS newline_val`
	err := s.Run(true, false)
	require.NoError(t, err)

	output := outBuf.buf.String()
	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "hello,world", records[1][0])
	assert.Equal(t, `say "hi"`, records[1][1])
	assert.Equal(t, "line1\nline2", records[1][2])
}

func TestCSVMultipleRows(t *testing.T) {
	s, outBuf, _ := setupCSVCmd(t)

	s.Query = "SELECT name, database_id FROM sys.databases WHERE database_id <= 4 ORDER BY database_id"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := outBuf.buf.String()
	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(records), 2, "should have header + at least 1 row")
	assert.Equal(t, []string{"name", "database_id"}, records[0])
	// First row should be master with database_id 1
	assert.Equal(t, "master", records[1][0])
	assert.Equal(t, "1", records[1][1])
}

func TestCSVMultiResultSet(t *testing.T) {
	s, outBuf, _ := setupCSVCmd(t)

	s.Query = "SELECT 1 AS a; SELECT 'x' AS b"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := outBuf.buf.String()
	// Should contain both CSV result sets (each with its own header)
	assert.Contains(t, output, "a\n")
	assert.Contains(t, output, "1\n")
	assert.Contains(t, output, "b\n")
	assert.Contains(t, output, "x\n")
}

// TestCSVMessagesNotInOutputFile verifies that when -o redirects output to a
// file but no explicit error writer is set (the default), messages like
// "rows affected" go to stderr instead of polluting the output file.
func TestCSVMessagesNotInOutputFile(t *testing.T) {
	s, outBuf := setupSqlCmdWithMemoryOutput(t)
	s.Format = NewCSVFormatter()
	// Do NOT call s.SetError — this simulates the `-o file` case where only
	// output is redirected. GetError() should default to os.Stderr, keeping
	// the output buffer clean.

	s.Query = "PRINT 'noise'; SELECT 1 AS val"
	err := s.Run(true, false)
	require.NoError(t, err)

	output := outBuf.buf.String()
	assert.NotContains(t, output, "noise", "messages must not appear in output")

	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2, "should have header + 1 data row")
	assert.Equal(t, []string{"val"}, records[0])
	assert.Equal(t, []string{"1"}, records[1])
}

func TestCSVMessagesToStderr(t *testing.T) {
	s, outBuf, errBuf := setupCSVCmd(t)

	s.Query = "PRINT 'hello from print'; SELECT 1 AS val"
	err := s.Run(true, false)
	require.NoError(t, err)

	// stdout should only have CSV data
	output := outBuf.buf.String()
	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, []string{"val"}, records[0])

	// stderr should have the PRINT message
	assert.Contains(t, errBuf.buf.String(), "hello from print")
}
