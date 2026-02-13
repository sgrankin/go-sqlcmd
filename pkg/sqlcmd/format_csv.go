// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"database/sql"
	"encoding/csv"
	"io"
)

type csvFormatter struct {
	out       io.Writer
	err       io.Writer
	vars      *Variables
	cols      []*sql.ColumnType
	scales    []int
	writer    *csv.Writer
	rawErrors bool
}

// NewCSVFormatter returns a Formatter that outputs results as CSV.
// Column headers are written as the first row. Messages and errors go to stderr.
func NewCSVFormatter(opts ...FormatterOption) Formatter {
	options := resolveFormatterOptions(opts)
	return &csvFormatter{rawErrors: options.rawErrors}
}

func (f *csvFormatter) BeginBatch(_ string, vars *Variables, out io.Writer, err io.Writer) {
	f.out = out
	f.err = err
	f.vars = vars
}

func (f *csvFormatter) EndBatch() {}

func (f *csvFormatter) BeginResultSet(cols []*sql.ColumnType) {
	f.cols = cols
	f.scales = make([]int, len(cols))
	for i, c := range cols {
		_, s, ok := c.DecimalSize()
		if ok {
			f.scales[i] = int(s)
		}
	}
	f.writer = csv.NewWriter(f.out)

	// Write header row
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Name()
	}
	_ = f.writer.Write(header)
}

func (f *csvFormatter) EndResultSet() {
	if f.writer != nil {
		f.writer.Flush()
		f.writer = nil
	}
}

func (f *csvFormatter) AddRow(rows *sql.Rows) string {
	values, err := scanRowStrings(rows, f.cols, f.scales)
	if err != nil {
		f.writeErr(err.Error())
		return ""
	}
	retval := ""
	if len(values) > 0 {
		retval = values[0]
	}
	// Replace "NULL" sentinel with empty string for CSV
	for i, v := range values {
		if v == "NULL" {
			values[i] = ""
		}
	}
	_ = f.writer.Write(values)
	return retval
}

func (f *csvFormatter) AddMessage(msg string) {
	f.writeErr(msg)
}

func (f *csvFormatter) AddError(err error) {
	if message := formatError(err, f.vars, f.rawErrors); message != "" && f.err != nil {
		_, _ = io.WriteString(f.err, message)
	}
}

func (f *csvFormatter) XmlMode(_ bool) {}

func (f *csvFormatter) IsXmlMode() bool { return false }

func (f *csvFormatter) writeErr(msg string) {
	if f.err != nil {
		_, _ = io.WriteString(f.err, msg+SqlcmdEol)
	}
}
