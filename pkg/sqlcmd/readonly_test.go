// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckReadOnly(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		allowExec bool
		wantErr   bool
		wantKW    string // expected keyword in error
	}{
		// Basic allowed statements
		{name: "SELECT", query: "SELECT 1", wantErr: false},
		{name: "SET", query: "SET NOCOUNT ON", wantErr: false},
		{name: "DECLARE", query: "DECLARE @x INT = 1", wantErr: false},
		{name: "PRINT", query: "PRINT 'hello'", wantErr: false},
		{name: "USE", query: "USE master", wantErr: false},
		{name: "IF", query: "IF 1=1 SELECT 1", wantErr: false},
		{name: "WHILE", query: "WHILE 1=0 SELECT 1", wantErr: false},
		{name: "RETURN", query: "RETURN", wantErr: false},
		{name: "THROW", query: "THROW 50000, 'err', 1", wantErr: false},
		{name: "RAISERROR", query: "RAISERROR('err', 16, 1)", wantErr: false},
		{name: "WAITFOR", query: "WAITFOR DELAY '00:00:01'", wantErr: false},
		{name: "BREAK", query: "BREAK", wantErr: false},
		{name: "CONTINUE", query: "CONTINUE", wantErr: false},
		{name: "GOTO", query: "GOTO label1", wantErr: false},
		{name: "TRY", query: "TRY", wantErr: false},
		{name: "CATCH", query: "CATCH", wantErr: false},

		// Basic rejected statements
		{name: "INSERT", query: "INSERT INTO t VALUES (1)", wantErr: true, wantKW: "INSERT"},
		{name: "UPDATE", query: "UPDATE t SET x = 1", wantErr: true, wantKW: "UPDATE"},
		{name: "DELETE", query: "DELETE FROM t", wantErr: true, wantKW: "DELETE"},
		{name: "DROP", query: "DROP TABLE t", wantErr: true, wantKW: "DROP"},
		{name: "ALTER", query: "ALTER TABLE t ADD x INT", wantErr: true, wantKW: "ALTER"},
		{name: "CREATE table", query: "CREATE TABLE t (x INT)", wantErr: true, wantKW: "CREATE"},
		{name: "TRUNCATE", query: "TRUNCATE TABLE t", wantErr: true, wantKW: "TRUNCATE"},
		{name: "MERGE", query: "MERGE INTO t USING s ON t.id=s.id WHEN MATCHED THEN DELETE;", wantErr: true, wantKW: "MERGE"},
		{name: "GRANT", query: "GRANT SELECT ON t TO u", wantErr: true, wantKW: "GRANT"},
		{name: "REVOKE", query: "REVOKE SELECT ON t FROM u", wantErr: true, wantKW: "REVOKE"},
		{name: "DENY", query: "DENY SELECT ON t TO u", wantErr: true, wantKW: "DENY"},
		{name: "BULK", query: "BULK INSERT t FROM 'file'", wantErr: true, wantKW: "BULK"},
		{name: "BACKUP", query: "BACKUP DATABASE db TO DISK='f'", wantErr: true, wantKW: "BACKUP"},
		{name: "RESTORE", query: "RESTORE DATABASE db FROM DISK='f'", wantErr: true, wantKW: "RESTORE"},
		{name: "DBCC", query: "DBCC CHECKDB", wantErr: true, wantKW: "DBCC"},
		{name: "SHUTDOWN", query: "SHUTDOWN", wantErr: true, wantKW: "SHUTDOWN"},
		{name: "KILL", query: "KILL 52", wantErr: true, wantKW: "KILL"},
		{name: "COMMIT", query: "COMMIT", wantErr: true, wantKW: "COMMIT"},
		{name: "ROLLBACK", query: "ROLLBACK", wantErr: true, wantKW: "ROLLBACK"},
		{name: "SAVE", query: "SAVE TRANSACTION sp1", wantErr: true, wantKW: "SAVE"},

		// EXEC — conditional
		{name: "EXEC rejected default", query: "EXEC sp_help", wantErr: true, wantKW: "EXEC"},
		{name: "EXECUTE rejected default", query: "EXECUTE sp_help", wantErr: true, wantKW: "EXECUTE"},
		{name: "EXEC allowed", query: "EXEC sp_help", allowExec: true, wantErr: false},
		{name: "EXECUTE allowed", query: "EXECUTE sp_help", allowExec: true, wantErr: false},

		// Comments — keywords inside comments should be ignored
		{name: "keyword in line comment", query: "-- DELETE FROM t\nSELECT 1", wantErr: false},
		{name: "keyword in block comment", query: "/* DELETE FROM t */ SELECT 1", wantErr: false},
		{name: "keyword in nested block comment", query: "/* /* DELETE */ */ SELECT 1", wantErr: false},
		{name: "only comment", query: "-- just a comment", wantErr: false},
		{name: "block comment only", query: "/* nothing here */", wantErr: false},

		// Quoted strings — keywords inside quotes should be ignored
		{name: "keyword in single quotes", query: "SELECT 'DELETE FROM t'", wantErr: false},
		{name: "keyword in double quotes", query: `SELECT "DELETE" FROM t`, wantErr: false},
		{name: "keyword in brackets", query: "SELECT [DELETE] FROM t", wantErr: false},
		{name: "escaped single quotes", query: "SELECT 'it''s DELETE'", wantErr: false},

		// Multi-statement
		{name: "safe multi-statement", query: "SELECT 1; SELECT 2", wantErr: false},
		{name: "second statement destructive", query: "SELECT 1; DELETE FROM t", wantErr: true, wantKW: "DELETE"},
		{name: "first statement destructive", query: "DROP TABLE t; SELECT 1", wantErr: true, wantKW: "DROP"},

		// CTEs (WITH)
		{name: "CTE with SELECT", query: "WITH cte AS (SELECT 1) SELECT * FROM cte", wantErr: false},
		{name: "CTE with DELETE", query: "WITH cte AS (SELECT 1) DELETE FROM t", wantErr: true, wantKW: "DELETE"},
		{name: "CTE with UPDATE", query: "WITH cte AS (SELECT 1) UPDATE t SET x = 1", wantErr: true, wantKW: "UPDATE"},
		{name: "CTE with INSERT", query: "WITH cte AS (SELECT 1) INSERT INTO t SELECT * FROM cte", wantErr: true, wantKW: "INSERT"},
		{name: "CTE with MERGE", query: "WITH cte AS (SELECT 1) MERGE INTO t USING cte ON 1=1 WHEN MATCHED THEN DELETE;", wantErr: true, wantKW: "MERGE"},

		// BEGIN/END blocks vs BEGIN TRAN
		{name: "BEGIN END block", query: "BEGIN SELECT 1 END", wantErr: false},
		{name: "BEGIN TRAN", query: "BEGIN TRAN", wantErr: true, wantKW: "BEGIN TRAN"},
		{name: "BEGIN TRANSACTION", query: "BEGIN TRANSACTION", wantErr: true, wantKW: "BEGIN TRANSACTION"},
		{name: "begin tran lowercase", query: "begin tran", wantErr: true, wantKW: "BEGIN TRAN"},

		// Temp tables
		{name: "CREATE TABLE #tmp", query: "CREATE TABLE #tmp (x INT)", wantErr: false},
		{name: "CREATE TABLE ##global_tmp", query: "CREATE TABLE ##global_tmp (x INT)", wantErr: false},
		{name: "CREATE TABLE real", query: "CREATE TABLE real_table (x INT)", wantErr: true, wantKW: "CREATE"},
		{name: "CREATE INDEX", query: "CREATE INDEX ix ON t(x)", wantErr: true, wantKW: "CREATE"},
		{name: "CREATE PROC", query: "CREATE PROCEDURE sp AS SELECT 1", wantErr: true, wantKW: "CREATE"},

		// SELECT INTO
		{name: "SELECT INTO #tmp", query: "SELECT * INTO #tmp FROM t", wantErr: false},
		{name: "SELECT INTO ##tmp", query: "SELECT * INTO ##tmp FROM t", wantErr: false},
		{name: "SELECT INTO real table", query: "SELECT * INTO real_table FROM t", wantErr: true, wantKW: "SELECT INTO"},
		{name: "SELECT without INTO", query: "SELECT * FROM t", wantErr: false},
		{name: "SELECT with subquery", query: "SELECT (SELECT 1) AS x", wantErr: false},

		// Whitespace and case variations
		{name: "leading whitespace", query: "   SELECT 1", wantErr: false},
		{name: "leading tabs", query: "\t\tSELECT 1", wantErr: false},
		{name: "leading newlines", query: "\n\nSELECT 1", wantErr: false},
		{name: "mixed case", query: "sElEcT 1", wantErr: false},
		{name: "mixed case DELETE", query: "DeLeTe FROM t", wantErr: true, wantKW: "DeLeTe"},

		// Edge cases
		{name: "empty string", query: "", wantErr: false},
		{name: "only whitespace", query: "   \t\n  ", wantErr: false},
		{name: "only semicolons", query: ";;;", wantErr: false},
		{name: "semicolons and whitespace", query: " ; ; ; ", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckReadOnly(tt.query, tt.allowExec)
			if tt.wantErr {
				require.Error(t, err)
				var roErr *ReadOnlyError
				require.ErrorAs(t, err, &roErr)
				assert.True(t, strings.EqualFold(tt.wantKW, roErr.Keyword),
					"expected keyword %q, got %q", tt.wantKW, roErr.Keyword)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckReadOnlyError(t *testing.T) {
	err := CheckReadOnly("DELETE FROM t", false)
	require.Error(t, err)

	var roErr *ReadOnlyError
	require.ErrorAs(t, err, &roErr)
	assert.Contains(t, roErr.Error(), "read-only mode rejected")
	assert.Contains(t, roErr.Error(), "--rw")
	assert.True(t, roErr.IsSqlcmdErr())
}

func TestReadOnlyGoCommand(t *testing.T) {
	// Integration test: goCommand with ReadOnly=true rejects a destructive batch
	// without needing a database connection.
	s, buf := setupSqlcmdWithMemoryOutput(t)
	s.ReadOnly = true

	s.batch.Reset([]rune("DELETE FROM t"))
	s.batch.Next()

	err := s.Cmd["GO"].action(s, []string{}, 1)
	assert.NoError(t, err) // non-fatal in non-ExitOnError mode

	output := buf.String()
	assert.Contains(t, output, "read-only mode rejected")
}

func TestReadOnlyGoCommandExitOnError(t *testing.T) {
	s, _ := setupSqlcmdWithMemoryOutput(t)
	s.ReadOnly = true
	s.Connect.ExitOnError = true

	s.batch.Reset([]rune("DELETE FROM t"))
	s.batch.Next()

	err := s.Cmd["GO"].action(s, []string{}, 1)
	require.Error(t, err)
	assert.Equal(t, 1, s.Exitcode)
}

func TestReadOnlyAllowsSafeStatements(t *testing.T) {
	// Verify that safe statements pass the read-only check directly.
	assert.NoError(t, CheckReadOnly("SELECT 1", false))
	assert.NoError(t, CheckReadOnly("SET NOCOUNT ON", false))
	assert.NoError(t, CheckReadOnly("DECLARE @x INT", false))
	// SET STATISTICS XML ON is used by --plan-file (via ExecContext, not in the batch)
	// but verify it would pass read-only anyway since SET is allowed
	assert.NoError(t, CheckReadOnly("SET STATISTICS XML ON", false))
}

// setupSqlcmdWithMemoryOutput creates a Sqlcmd with in-memory error output for testing.
func setupSqlcmdWithMemoryOutput(t *testing.T) (*Sqlcmd, *memBuffer) {
	t.Helper()
	vars := InitializeVariables(false)
	s := New(nil, t.TempDir(), vars)
	s.Connect = &ConnectSettings{}
	buf := &memBuffer{}
	s.SetError(buf)
	return s, buf
}

// memBuffer is a simple in-memory WriteCloser for capturing output.
type memBuffer struct {
	data []byte
}

func (b *memBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *memBuffer) Close() error { return nil }

func (b *memBuffer) String() string { return string(b.data) }
