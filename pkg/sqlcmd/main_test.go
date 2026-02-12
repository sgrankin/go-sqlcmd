// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"os"
	"testing"

	"github.com/microsoft/go-sqlcmd/internal/sqlservertest"
)

func TestMain(m *testing.M) {
	cleanup := sqlservertest.SetupForTestMain()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
