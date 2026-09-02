// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

import (
	"io"

	"github.com/sgrankin/go-sqlcmd/pkg/sqlcmd"
)

// mssql implements for SQL Server
type mssql struct {
	sqlcmd        *sqlcmd.Sqlcmd
	console       sqlcmd.Console
	readOnly      bool
	allowExec     bool
	planFile      string
	planBuffer    io.Writer
	estimatedPlan bool
	format        string
}

// mock impoements for unit testing which uses a Hello World container (no
// SQL)
type mock struct{}
