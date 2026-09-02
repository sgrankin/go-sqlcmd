// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sql

import "io"

type SqlOptions struct {
	UnitTesting   bool
	ReadOnly      bool
	AllowExec     bool
	PlanFile      string
	PlanBuffer    io.Writer
	EstimatedPlan bool
	Format        string
}

func New(options SqlOptions) Sql {
	if options.UnitTesting {
		return &mock{}
	} else {
		return &mssql{
			readOnly:      options.ReadOnly,
			allowExec:     options.AllowExec,
			planFile:      options.PlanFile,
			planBuffer:    options.PlanBuffer,
			estimatedPlan: options.EstimatedPlan,
			format:        options.Format,
		}
	}
}
