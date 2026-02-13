package plan

import (
	"encoding/json"
	"io"
)

// FormatJSON writes the analysis result as JSON to the writer.
func FormatJSON(w io.Writer, result *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
