// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package main

import (
	_ "fmt"

	_ "github.com/sgrankin/go-sqlcmd/pkg/sqlcmd" // want "Internal packages should not import \"github.com/sgrankin/go-sqlcmd/pkg/sqlcmd\""
)

var X = 1
