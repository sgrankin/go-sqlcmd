// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package root

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sgrankin/go-sqlcmd/internal/cmdparser"
	"github.com/sgrankin/go-sqlcmd/internal/config"
	"github.com/sgrankin/go-sqlcmd/internal/localizer"
	"github.com/sgrankin/go-sqlcmd/internal/pal"
	"github.com/sgrankin/go-sqlcmd/internal/plan"
	"github.com/sgrankin/go-sqlcmd/internal/sql"
	"github.com/sgrankin/go-sqlcmd/pkg/sqlcmd"
)

// Query defines the `sqlcmd query` command
type Query struct {
	cmdparser.Cmd

	text        string
	database    string
	rw          bool
	allowExec   bool
	planFile    string
	format      string
	analyzeFile string
	summary     bool
}

func (c *Query) DefineCommand(...cmdparser.CommandOptions) {
	options := cmdparser.CommandOptions{
		Use:   "query",
		Short: localizer.Sprintf("Run a query against the current context"),
		Examples: []cmdparser.ExampleOptions{
			{Description: localizer.Sprintf("Run a query"), Steps: []string{
				`sqlcmd query "SELECT @@SERVERNAME"`,
				`sqlcmd query --text "SELECT @@SERVERNAME"`,
				`sqlcmd query --query "SELECT @@SERVERNAME"`,
			}},
			{Description: localizer.Sprintf("Run a query using [%s] database", "master"), Steps: []string{
				`sqlcmd query "SELECT DB_NAME()" --database master`,
			}},
			{Description: localizer.Sprintf("Set new default database"), Steps: []string{
				fmt.Sprintf(`sqlcmd query "ALTER LOGIN [%s] WITH DEFAULT_DATABASE = [tempdb]" --database master`,
					pal.UserName()),
			}},
			{Description: localizer.Sprintf("Run a query and analyze its execution plan"), Steps: []string{
				`sqlcmd query --analyze-file analysis.json "SELECT * FROM orders"`,
			}},
		},
		Run: c.run,
		FirstArgAlternativeForFlag: &cmdparser.AlternativeForFlagOptions{
			Flag:  "text",
			Value: &c.text,
		},
	}

	c.Cmd.DefineCommand(options)

	c.AddFlag(cmdparser.FlagOptions{
		String:    &c.text,
		Name:      "text",
		Shorthand: "t",
		Usage:     localizer.Sprintf("Command text to run")})

	// BUG(stuartpa): Decide on if --text or --query is best (or leave both for convenience)
	c.AddFlag(cmdparser.FlagOptions{
		String:    &c.text,
		Name:      "query",
		Shorthand: "q",
		Usage:     localizer.Sprintf("Command text to run")})

	c.AddFlag(cmdparser.FlagOptions{
		String:    &c.database,
		Name:      "database",
		Shorthand: "d",
		Usage:     localizer.Sprintf("Database to use")})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.rw,
		Name:  "rw",
		Usage: localizer.Sprintf("Disable read-only mode, allowing destructive SQL statements")})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.allowExec,
		Name:  "allow-exec",
		Usage: localizer.Sprintf("Allow EXEC/EXECUTE statements in read-only mode")})

	c.AddFlag(cmdparser.FlagOptions{
		String: &c.planFile,
		Name:   "plan-file",
		Usage:  localizer.Sprintf("Write execution plan XML to the specified file")})

	c.AddFlag(cmdparser.FlagOptions{
		String: &c.format,
		Name:   "format",
		Usage:  localizer.Sprintf("Output format: default, csv, jsonl")})

	c.AddFlag(cmdparser.FlagOptions{
		String: &c.analyzeFile,
		Name:   "analyze-file",
		Usage:  localizer.Sprintf("Analyze the execution plan and write JSON analysis to the specified file")})

	c.AddFlag(cmdparser.FlagOptions{
		Bool:  &c.summary,
		Name:  "summary",
		Usage: localizer.Sprintf("Print concise summary to stdout (row/column counts, analysis highlights, file paths)")})
}

func (c *Query) run() {
	endpoint, user := config.CurrentContext()

	var planBuf *bytes.Buffer
	if c.analyzeFile != "" {
		planBuf = &bytes.Buffer{}
	}

	s := sql.New(sql.SqlOptions{
		ReadOnly:   !c.rw,
		AllowExec:  c.allowExec,
		PlanFile:   c.planFile,
		PlanBuffer: planBuf,
		Format:     c.format,
	})
	if c.text == "" {
		s.Connect(endpoint, user, sql.ConnectOptions{Database: c.database, Interactive: true})
	} else {
		s.Connect(endpoint, user, sql.ConnectOptions{Database: c.database, Interactive: false})
	}

	s.Query(c.text)

	var analysisResult *plan.Result
	if c.analyzeFile != "" && planBuf.Len() > 0 {
		plans, err := plan.Parse(planBuf.Bytes())
		c.CheckErr(err)
		analysisResult = plan.Analyze(plans)
		af, afErr := os.Create(c.analyzeFile)
		c.CheckErr(afErr)
		c.CheckErr(plan.FormatJSON(af, analysisResult))
		af.Close()
	}

	if c.summary {
		c.printSummary(os.Stdout, s.ResultSets(), analysisResult)
	}
}

func (c *Query) printSummary(w io.Writer, resultSets []sqlcmd.ResultSetInfo, analysisResult *plan.Result) {
	for _, rs := range resultSets {
		colPreview := ""
		if len(rs.Columns) > 0 {
			cols := rs.Columns
			if len(cols) > 5 {
				colPreview = fmt.Sprintf(" [%s, ...]", strings.Join(cols[:5], ", "))
			} else {
				colPreview = fmt.Sprintf(" [%s]", strings.Join(cols, ", "))
			}
		}
		fmt.Fprintf(w, "%d rows, %d columns%s\n", rs.RowCount, len(rs.Columns), colPreview)
	}

	if c.planFile != "" {
		fmt.Fprintf(w, "Plan: %s\n", c.planFile)
	}
	if c.analyzeFile != "" {
		fmt.Fprintf(w, "Analysis: %s\n", c.analyzeFile)
	}

	if analysisResult != nil {
		plan.FormatSummary(w, analysisResult)
	}
}
