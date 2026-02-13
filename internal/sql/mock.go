// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

import (
	"io"

	. "github.com/sgrankin/go-sqlcmd/cmd/sqlcmd/sqlconfig"
)

// Connect is a mock implementation used to speed up unit testing of other units
func (m *mock) Connect(
	endpoint Endpoint,
	user *User,
	options ConnectOptions,
) {
}

// Query is a mock implementation used to speed up unit testing of other units
func (m *mock) Query(text string) {
}

func (m *mock) QueryToWriter(text string, w io.Writer) {
}

func (m *mock) ScalarString(query string) string {
	return ""
}
