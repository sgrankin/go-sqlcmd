// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"database/sql"
	"encoding/json"
	"io"
)

type jsonlFormatter struct {
	out       io.Writer
	err       io.Writer
	vars      *Variables
	cols      []*sql.ColumnType
	scales    []int
	colNames  []string
	rawErrors bool
}

// NewJSONLFormatter returns a Formatter that outputs results as JSON Lines.
// Each row is written as a single JSON object on its own line.
// Type fidelity: numbers stay numbers, bools stay bools, nulls are JSON null.
func NewJSONLFormatter(opts ...FormatterOption) Formatter {
	options := resolveFormatterOptions(opts)
	return &jsonlFormatter{rawErrors: options.rawErrors}
}

func (f *jsonlFormatter) BeginBatch(_ string, vars *Variables, out io.Writer, err io.Writer) {
	f.out = out
	f.err = err
	f.vars = vars
}

func (f *jsonlFormatter) EndBatch() {}

func (f *jsonlFormatter) BeginResultSet(cols []*sql.ColumnType) {
	f.cols = cols
	f.scales = make([]int, len(cols))
	f.colNames = make([]string, len(cols))
	for i, c := range cols {
		f.colNames[i] = c.Name()
		_, s, ok := c.DecimalSize()
		if ok {
			f.scales[i] = int(s)
		}
	}
}

func (f *jsonlFormatter) EndResultSet() {}

func (f *jsonlFormatter) AddRow(rows *sql.Rows) string {
	values, err := scanRowTyped(rows, f.cols, f.scales)
	if err != nil {
		f.writeErr(err.Error())
		return ""
	}
	retval := ""
	if len(values) > 0 && values[0] != nil {
		switch v := values[0].(type) {
		case string:
			retval = v
		}
	}

	obj := make(map[string]interface{}, len(f.colNames))
	for i, name := range f.colNames {
		obj[name] = values[i]
	}
	data, err := json.Marshal(obj)
	if err != nil {
		f.writeErr(err.Error())
		return retval
	}
	_, _ = f.out.Write(data)
	_, _ = io.WriteString(f.out, "\n")
	return retval
}

func (f *jsonlFormatter) AddMessage(msg string) {
	f.writeErr(msg)
}

func (f *jsonlFormatter) AddError(err error) {
	if message := formatError(err, f.vars, f.rawErrors); message != "" && f.err != nil {
		_, _ = io.WriteString(f.err, message)
	}
}

func (f *jsonlFormatter) XmlMode(_ bool) {}

func (f *jsonlFormatter) IsXmlMode() bool { return false }

func (f *jsonlFormatter) writeErr(msg string) {
	if f.err != nil {
		_, _ = io.WriteString(f.err, msg+SqlcmdEol)
	}
}
