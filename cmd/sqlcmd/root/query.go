// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package root

import (
	"bytes"
	"fmt"
	"os"

	"github.com/sgrankin/go-sqlcmd/internal/cmdparser"
	"github.com/sgrankin/go-sqlcmd/internal/config"
	"github.com/sgrankin/go-sqlcmd/internal/localizer"
	"github.com/sgrankin/go-sqlcmd/internal/pal"
	"github.com/sgrankin/go-sqlcmd/internal/plan"
	"github.com/sgrankin/go-sqlcmd/internal/sql"
)

// Query defines the `sqlcmd query` command
type Query struct {
	cmdparser.Cmd

	text      string
	database  string
	rw        bool
	allowExec bool
	planFile  string
	format    string
	analyze   bool
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
				`sqlcmd query --analyze "SELECT * FROM orders"`,
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
		Bool:  &c.analyze,
		Name:  "analyze",
		Usage: localizer.Sprintf("Analyze the execution plan after running the query")})
}

func (c *Query) run() {
	endpoint, user := config.CurrentContext()

	var planBuf *bytes.Buffer
	if c.analyze {
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

	if c.analyze && planBuf.Len() > 0 {
		plans, err := plan.Parse(planBuf.Bytes())
		c.CheckErr(err)
		result := plan.Analyze(plans)
		fmt.Fprintln(os.Stdout)
		plan.FormatText(os.Stdout, result)
	}
}
