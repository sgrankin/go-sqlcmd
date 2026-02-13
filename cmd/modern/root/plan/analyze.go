package plan

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/microsoft/go-sqlcmd/internal/cmdparser"
	"github.com/microsoft/go-sqlcmd/internal/config"
	"github.com/microsoft/go-sqlcmd/internal/localizer"
	"github.com/microsoft/go-sqlcmd/internal/plan"
	"github.com/microsoft/go-sqlcmd/internal/sql"
)

// Analyze defines the `sqlcmd plan analyze` command
type Analyze struct {
	cmdparser.Cmd

	file      string
	query     string
	database  string
	rw        bool
	allowExec bool
	format    string
}

func (c *Analyze) DefineCommand(...cmdparser.CommandOptions) {
	options := cmdparser.CommandOptions{
		Use:   "analyze",
		Short: localizer.Sprintf("Analyze a SQL Server execution plan"),
		Examples: []cmdparser.ExampleOptions{
			{
				Description: localizer.Sprintf("Analyze a saved plan file"),
				Steps:       []string{"sqlcmd plan analyze plan.xml"},
			},
			{
				Description: localizer.Sprintf("Run a query and analyze its execution plan"),
				Steps:       []string{`sqlcmd plan analyze -Q "SELECT * FROM orders" --database mydb`},
			},
			{
				Description: localizer.Sprintf("Analyze with JSON output"),
				Steps:       []string{"sqlcmd plan analyze --format json plan.xml"},
			},
		},
		Run: c.run,
		FirstArgAlternativeForFlag: &cmdparser.AlternativeForFlagOptions{
			Flag:  "file",
			Value: &c.file,
		},
	}

	c.Cmd.DefineCommand(options)

	c.AddFlag(cmdparser.FlagOptions{
		String: &c.file,
		Name:   "file",
		Usage:  localizer.Sprintf("Path to execution plan XML file"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		String:    &c.query,
		Name:      "query",
		Shorthand: "Q",
		Usage:     localizer.Sprintf("SQL query to execute and analyze"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		String:    &c.database,
		Name:      "database",
		Shorthand: "d",
		Usage:     localizer.Sprintf("Database to use"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.rw,
		Name:  "rw",
		Usage: localizer.Sprintf("Disable read-only mode, allowing destructive SQL statements"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.allowExec,
		Name:  "allow-exec",
		Usage: localizer.Sprintf("Allow EXEC/EXECUTE statements in read-only mode"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		String: &c.format,
		Name:   "format",
		Usage:  localizer.Sprintf("Output format: text (default), json"),
	})
}

func (c *Analyze) run() {
	var data []byte

	if c.file != "" && c.query != "" {
		c.CheckErr(fmt.Errorf("cannot specify both a plan file and a query"))
		return
	}

	if c.file != "" {
		// Mode 1: Analyze from file
		var err error
		data, err = os.ReadFile(c.file)
		c.CheckErr(err)
	} else if c.query != "" {
		// Mode 2: Run query, capture plan, analyze
		data = c.captureQueryPlan()
	} else {
		c.CheckErr(fmt.Errorf("specify a plan file or use -Q to run a query"))
		return
	}

	plans, err := plan.Parse(data)
	c.CheckErr(err)

	result := plan.Analyze(plans)
	c.writeOutput(result)
}

func (c *Analyze) captureQueryPlan() []byte {
	endpoint, user := config.CurrentContext()

	var planBuf bytes.Buffer
	s := sql.New(c.sqlOptions(&planBuf))
	s.Connect(endpoint, user, sql.ConnectOptions{Database: c.database, Interactive: false})
	// Suppress data output by directing to discard
	s.QueryToWriter(c.query, io.Discard)

	return planBuf.Bytes()
}

func (c *Analyze) sqlOptions(planBuffer io.Writer) sql.SqlOptions {
	return sql.SqlOptions{
		ReadOnly:   !c.rw,
		AllowExec:  c.allowExec,
		PlanBuffer: planBuffer,
	}
}

func (c *Analyze) writeOutput(result *plan.Result) {
	switch c.format {
	case "json":
		c.CheckErr(plan.FormatJSON(os.Stdout, result))
	default:
		plan.FormatText(os.Stdout, result)
	}
}
