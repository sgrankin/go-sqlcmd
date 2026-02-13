// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

type SqlOptions struct {
	UnitTesting bool
	ReadOnly    bool
	AllowExec   bool
	PlanFile    string
	Format      string
}

func New(options SqlOptions) Sql {
	if options.UnitTesting {
		return &mock{}
	} else {
		return &mssql{
			readOnly:  options.ReadOnly,
			allowExec: options.AllowExec,
			planFile:  options.PlanFile,
			format:    options.Format,
		}
	}
}
