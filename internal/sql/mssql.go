// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sgrankin/go-sqlcmd/internal/buffer"
	"github.com/sgrankin/go-sqlcmd/pkg/console"

	"github.com/sgrankin/go-sqlcmd/cmd/sqlcmd/sqlconfig"
	"github.com/sgrankin/go-sqlcmd/pkg/sqlcmd"
)

// Connect is used to connect to a SQL Server using the specified endpoint
// and user details. The console parameter is used to output messages during
// the connection process. The function returns a Sqlcmd instance that can
// be used to run SQL commands on the server.
func (m *mssql) Connect(
	endpoint sqlconfig.Endpoint,
	user *sqlconfig.User,
	options ConnectOptions,
) {
	v := sqlcmd.InitializeVariables(true)
	if options.Interactive {
		m.console = console.NewConsole("")
		defer m.console.Close()
	} else {
		m.console = nil
	}
	m.sqlcmd = sqlcmd.New(m.console, "", v)
	m.sqlcmd.ReadOnly = m.readOnly
	m.sqlcmd.AllowExec = m.allowExec
	switch {
	case m.planFile != "" && m.planBuffer != nil:
		pf, pfErr := os.Create(m.planFile)
		if pfErr != nil {
			checkErr(fmt.Errorf("failed to create plan file '%s': %w", m.planFile, pfErr))
		}
		m.sqlcmd.PlanFile = io.MultiWriter(pf, m.planBuffer)
	case m.planFile != "":
		pf, pfErr := os.Create(m.planFile)
		if pfErr != nil {
			checkErr(fmt.Errorf("failed to create plan file '%s': %w", m.planFile, pfErr))
		}
		m.sqlcmd.PlanFile = pf
	case m.planBuffer != nil:
		m.sqlcmd.PlanFile = m.planBuffer
	}
	switch m.format {
	case "csv":
		m.sqlcmd.Format = sqlcmd.NewCSVFormatter()
	case "jsonl":
		m.sqlcmd.Format = sqlcmd.NewJSONLFormatter()
	default:
		m.sqlcmd.Format = sqlcmd.NewSQLCmdDefaultFormatter(v, false, sqlcmd.ControlIgnore)
	}
	connect := sqlcmd.ConnectSettings{
		ServerName: fmt.Sprintf(
			"%s,%#v",
			endpoint.EndpointDetails.Address,
			endpoint.EndpointDetails.Port),
		ApplicationName: "sqlcmd",
	}

	if options.Database != "" {
		connect.Database = options.Database
	}

	if user == nil {
		connect.UseTrustedConnection = true
	} else {
		if user.AuthenticationType == "basic" {
			connect.UseTrustedConnection = false
			connect.UserName = user.BasicAuth.Username
			connect.Password = decryptCallback(
				user.BasicAuth.Password,
				user.BasicAuth.PasswordEncryption,
			)
		} else {
			panic("Authentication not supported")
		}
	}

	trace("Connecting to server %v", connect.ServerName)
	err := m.sqlcmd.ConnectDb(&connect, true)
	checkErr(err)
}

// Query is helper function that allows running a given SQL query on a
// provided sqlcmd.Sqlcmd object. It takes the sqlcmd.Sqlcmd object and the
// query text as inputs, and runs the query using the Run method of
// the sqlcmd.Sqlcmd object. It sets the standard output and standard error
// to be the same as the current process, and returns the error if any occurred
// during the execution of the query.
func (m *mssql) Query(text string) {
	if m.console == nil {
		m.sqlcmd.Query = text
		m.sqlcmd.SetOutput(os.Stdout)
		m.sqlcmd.SetError(os.Stderr)
		trace("Running query: %v", text)
		err := m.sqlcmd.Run(true, false)
		checkErr(err)
	} else {
		// sqlcmd prints the ErrCtrlC message before returning
		// In modern mode we do not exit the process on ctrl-c during interactive mode
		err := m.sqlcmd.Run(false, true)
		if err != sqlcmd.ErrCtrlC {
			checkErr(err)
		}
	}
}

func (m *mssql) QueryToWriter(text string, w io.Writer) {
	m.sqlcmd.Query = text
	m.sqlcmd.SetOutput(nopWriteCloser{w})
	m.sqlcmd.SetError(os.Stderr)
	trace("Running query: %v", text)
	err := m.sqlcmd.Run(true, false)
	checkErr(err)
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func (m *mssql) ResultSets() []sqlcmd.ResultSetInfo {
	if m.sqlcmd == nil {
		return nil
	}
	return m.sqlcmd.ResultSets
}

func (m *mssql) ScalarString(query string) string {
	buf := buffer.NewMemoryBuffer()
	defer func() { _ = buf.Close() }()

	m.sqlcmd.Query = query
	m.sqlcmd.SetOutput(buf)
	m.sqlcmd.SetError(os.Stderr)

	trace("Running query: %v", query)
	err := m.sqlcmd.Run(true, false)
	checkErr(err)

	return strings.TrimRight(buf.String(), "\r\n")
}
