// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

import "testing"
import . "github.com/sgrankin/go-sqlcmd/cmd/sqlcmd/sqlconfig"

func TestMockConnect(t *testing.T) {
	mockObj := mock{}
	mockObj.Connect(Endpoint{}, nil, ConnectOptions{})
}

func TestMockQuery(t *testing.T) {
	mockObj := mock{}
	mockObj.Query("")
}
