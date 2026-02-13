package plan

import (
	"bytes"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/sgrankin/go-sqlcmd/internal/sqlservertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	cleanup := sqlservertest.SetupForTestMain()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestFormatTextRealPlan(t *testing.T) {
	server := os.Getenv("SQLCMDSERVER")
	if server == "" {
		t.Skip("SQLCMDSERVER not set (no SQL Server available)")
	}
	user := os.Getenv("SQLCMDUSER")
	pass := os.Getenv("SQLCMDPASSWORD")

	// SQLCMDSERVER uses "host,port" format; go-mssqldb URL needs "host:port"
	dsn := "sqlserver://" + user + ":" + pass + "@" + strings.Replace(server, ",", ":", 1) + "?database=tempdb&encrypt=disable"
	db, err := sql.Open("sqlserver", dsn)
	require.NoError(t, err)
	defer db.Close()

	// Use a single connection so temp tables persist across statements
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer conn.Close()

	// Create a table with enough data to trigger a memory grant (sort/hash)
	_, err = conn.ExecContext(t.Context(), `
		IF OBJECT_ID('tempdb..#plan_test', 'U') IS NOT NULL DROP TABLE #plan_test;
		CREATE TABLE #plan_test (id INT, val INT, txt VARCHAR(100));
		;WITH nums AS (
			SELECT TOP 1000 ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS n
			FROM sys.all_objects a CROSS JOIN sys.all_objects b
		)
		INSERT INTO #plan_test SELECT n, n % 100, REPLICATE('x', 50) FROM nums;
	`)
	require.NoError(t, err)

	// Capture actual execution plan via SET STATISTICS XML ON
	_, err = conn.ExecContext(t.Context(), "SET STATISTICS XML ON")
	require.NoError(t, err)

	// Query that forces a sort (memory grant) and hash match
	rows, err := conn.QueryContext(t.Context(), `
		SELECT a.id, a.val, b.val AS val2
		FROM #plan_test a
		JOIN #plan_test b ON a.val = b.val
		ORDER BY a.id
		OPTION (MAXDOP 1)
	`)
	require.NoError(t, err)

	// Read through all result sets to find the XML plan
	var planXML string
	for {
		for rows.Next() {
			// Try to scan as string — the plan result set has one column
			cols, _ := rows.Columns()
			if len(cols) == 1 {
				var s string
				if err := rows.Scan(&s); err == nil {
					if len(s) > 0 && s[0] == '<' {
						planXML = s
					}
				}
			}
		}
		if !rows.NextResultSet() {
			break
		}
	}
	require.NoError(t, rows.Err())
	rows.Close()

	require.NotEmpty(t, planXML, "should have captured execution plan XML")

	// Parse and analyze
	plans, err := Parse([]byte(planXML))
	require.NoError(t, err)
	require.NotEmpty(t, plans)

	result := Analyze(plans)
	require.NotEmpty(t, result.Statements)

	stmt := result.Statements[0]
	assert.True(t, stmt.HasActualInfo, "real plan should have runtime info")
	assert.NotNil(t, stmt.Root)
	assert.Greater(t, stmt.Root.ActualRows, int64(0))

	// Verify MemoryGrant has GrantedMemory (sort/hash requires memory)
	assert.NotEmpty(t, stmt.MemoryGrant, "should have MemoryGrantInfo")
	assert.Contains(t, stmt.MemoryGrant, "GrantedMemory",
		"actual plan with sort/hash should have GrantedMemory; got keys: %v", keysOf(stmt.MemoryGrant))
	assert.Contains(t, stmt.MemoryGrant, "MaxUsedMemory",
		"actual plan should have MaxUsedMemory; got keys: %v", keysOf(stmt.MemoryGrant))

	// Verify FormatText renders the granted/used memory line
	var buf bytes.Buffer
	FormatText(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "granted")
	assert.Contains(t, output, "used")
	assert.Contains(t, output, "Operator Tree:")
	assert.Contains(t, output, "★")

	t.Logf("FormatText output:\n%s", output)
}

func keysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
