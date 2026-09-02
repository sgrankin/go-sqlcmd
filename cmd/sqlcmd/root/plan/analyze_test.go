package plan

import (
	"io"
	"testing"

	"github.com/sgrankin/go-sqlcmd/internal/cmdparser"
	"github.com/sgrankin/go-sqlcmd/internal/cmdparser/dependency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeQueryReadOnlyOptions(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantReadOnly  bool
		wantAllowExec bool
		wantEstimated bool
	}{
		{name: "read-only by default", wantReadOnly: true},
		{name: "allow exec", args: []string{"--allow-exec"}, wantReadOnly: true, wantAllowExec: true},
		{name: "read-write", args: []string{"--rw"}},
		{name: "estimated plan", args: []string{"--estimated"}, wantReadOnly: true, wantEstimated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := cmdparser.New[*Analyze](dependency.Options{})
			require.NoError(t, command.Command().ParseFlags(tt.args))

			options := command.sqlOptions(io.Discard)
			assert.Equal(t, tt.wantReadOnly, options.ReadOnly)
			assert.Equal(t, tt.wantAllowExec, options.AllowExec)
			assert.Equal(t, tt.wantEstimated, options.EstimatedPlan)
			assert.Equal(t, io.Discard, options.PlanBuffer)
		})
	}
}

func TestAnalyzeValidateInput(t *testing.T) {
	tests := []struct {
		name      string
		command   Analyze
		wantError string
	}{
		{name: "plan file", command: Analyze{file: "plan.xml"}},
		{name: "query", command: Analyze{query: "SELECT 1"}},
		{name: "estimated query", command: Analyze{query: "SELECT 1", estimated: true}},
		{
			name:      "file and query",
			command:   Analyze{file: "plan.xml", query: "SELECT 1"},
			wantError: "cannot specify both a plan file and a query",
		},
		{
			name:      "estimated file",
			command:   Analyze{file: "plan.xml", estimated: true},
			wantError: "--estimated requires -Q/--query",
		},
		{
			name:      "missing input",
			command:   Analyze{},
			wantError: "specify a plan file or use -Q to run a query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.command.validateInput()
			if tt.wantError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantError)
			}
		})
	}
}
