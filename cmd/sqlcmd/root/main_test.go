// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package root

import (
	"os"
	"testing"

	"github.com/sgrankin/go-sqlcmd/internal/sqlservertest"
)

func TestMain(m *testing.M) {
	cleanup := sqlservertest.SetupForTestMain()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
