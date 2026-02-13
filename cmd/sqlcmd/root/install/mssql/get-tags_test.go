// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package mssql

import (
	"github.com/sgrankin/go-sqlcmd/internal/cmdparser"
	"testing"
)

func TestEdgeGetTags(t *testing.T) {
	cmdparser.TestSetup(t)
	cmdparser.TestCmd[*GetTags]()
}
