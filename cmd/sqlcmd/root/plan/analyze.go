package plan

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/sgrankin/go-sqlcmd/internal/cmdparser"
	"github.com/sgrankin/go-sqlcmd/internal/config"
	"github.com/sgrankin/go-sqlcmd/internal/localizer"
	"github.com/sgrankin/go-sqlcmd/internal/plan"
	"github.com/sgrankin/go-sqlcmd/internal/sql"
)

// Analyze defines the `sqlcmd plan analyze` command
type Analyze struct {
	cmdparser.Cmd

	file       string
	query      string
	database   string
	rw         bool
	allowExec  bool
	format     string
	outputFile string
	summary    bool
	estimated  bool
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
				Description: localizer.Sprintf("Compile a query and analyze its estimated plan"),
				Steps:       []string{`sqlcmd plan analyze -Q "SELECT * FROM orders" --estimated`},
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
		Usage:     localizer.Sprintf("SQL query to analyze"),
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

	c.AddFlag(cmdparser.FlagOptions{
		String:    &c.outputFile,
		Name:      "output-file",
		Shorthand: "o",
		Usage:     localizer.Sprintf("Write analysis to the specified file (text by default, JSON with --format json)"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.summary,
		Name:  "summary",
		Usage: localizer.Sprintf("Print concise summary instead of full output"),
	})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.estimated,
		Name:  "estimated",
		Usage: localizer.Sprintf("Compile the query without executing it and analyze the estimated plan"),
	})
}

func (c *Analyze) run() {
	var data []byte

	c.CheckErr(c.validateInput())

	if c.file != "" {
		// Mode 1: Analyze from file
		var err error
		data, err = os.ReadFile(c.file)
		c.CheckErr(err)
	} else {
		// Mode 2: Run query, capture plan, analyze
		data = c.captureQueryPlan()
	}

	plans, err := plan.Parse(data)
	c.CheckErr(err)

	result := plan.Analyze(plans)
	c.writeOutput(result)
}

func (c *Analyze) validateInput() error {
	switch {
	case c.file != "" && c.query != "":
		return fmt.Errorf("cannot specify both a plan file and a query")
	case c.estimated && c.query == "":
		return fmt.Errorf("--estimated requires -Q/--query")
	case c.file == "" && c.query == "":
		return fmt.Errorf("specify a plan file or use -Q to run a query")
	default:
		return nil
	}
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
		ReadOnly:      !c.rw,
		AllowExec:     c.allowExec,
		PlanBuffer:    planBuffer,
		EstimatedPlan: c.estimated,
	}
}

func (c *Analyze) writeOutput(result *plan.Result) {
	if c.outputFile != "" {
		f, err := os.Create(c.outputFile)
		c.CheckErr(err)
		if c.format == "json" {
			c.CheckErr(plan.FormatJSON(f, result))
		} else {
			plan.FormatText(f, result)
		}
		f.Close()
	}

	switch {
	case c.summary:
		plan.FormatSummary(os.Stdout, result)
	case c.outputFile != "":
		// File already written, nothing more to stdout
	case c.format == "json":
		c.CheckErr(plan.FormatJSON(os.Stdout, result))
	default:
		plan.FormatText(os.Stdout, result)
	}
}
