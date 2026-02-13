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
	}{
		{name: "read-only by default", wantReadOnly: true},
		{name: "allow exec", args: []string{"--allow-exec"}, wantReadOnly: true, wantAllowExec: true},
		{name: "read-write", args: []string{"--rw"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := cmdparser.New[*Analyze](dependency.Options{})
			require.NoError(t, command.Command().ParseFlags(tt.args))

			options := command.sqlOptions(io.Discard)
			assert.Equal(t, tt.wantReadOnly, options.ReadOnly)
			assert.Equal(t, tt.wantAllowExec, options.AllowExec)
			assert.Equal(t, io.Discard, options.PlanBuffer)
		})
	}
}
