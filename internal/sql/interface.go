// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

import (
	"io"

	. "github.com/sgrankin/go-sqlcmd/cmd/sqlcmd/sqlconfig"
)

type Sql interface {
	Connect(endpoint Endpoint, user *User, options ConnectOptions)
	Query(text string)
	QueryToWriter(text string, w io.Writer)
	ScalarString(query string) string
}

type ConnectOptions struct {
	Database string

	Interactive bool
}
