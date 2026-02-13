package plan

import (
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ShowPlanXML is the root element of a SQL Server execution plan XML document.
type ShowPlanXML struct {
	XMLName    xml.Name    `xml:"ShowPlanXML"`
	Statements []StmtSimple `xml:"-"`
}

// StmtSimple represents a statement in the execution plan.
type StmtSimple struct {
	Attrs      map[string]string
	SetOptions map[string]string
	QueryPlan  *QueryPlan
}

// QueryPlan holds the query plan information for a statement.
type QueryPlan struct {
	Attrs         map[string]string
	MemoryGrant   map[string]string
	HardwareProps map[string]string
	Warnings      []xmlWarning
	TimeStats     map[string]string
	StatsRefCount int
	Root          *RelOp
}

// RelOp is a node in the operator tree.
type RelOp struct {
	NodeID     int
	PhysicalOp string
	LogicalOp  string
	EstRows    float64
	Threads    []threadInfo
	ObjectInfo string
	Warnings   []xmlWarning
	Children   []*RelOp
}

type threadInfo struct {
	ActualRows    int64
	ActualElapsed int64
}

type xmlWarning struct {
	Tag      string
	Attrs    map[string]string
	Children []xmlWarning
}

// Parse parses one or more ShowPlanXML documents from the given bytes.
// It handles raw XML, CSV-wrapped XML (from SSMS/Rider exports), and
// multiple plan documents separated by newlines.
func Parse(data []byte) ([]*ShowPlanXML, error) {
	text := string(data)
	text = extractFromCSV(text)
	chunks := splitPlanDocuments(text)

	var plans []*ShowPlanXML
	for _, chunk := range chunks {
		plan, err := parseSinglePlan([]byte(chunk))
		if err != nil {
			return nil, fmt.Errorf("parsing plan: %w", err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// extractFromCSV detects CSV-wrapped XML and extracts the XML content.
func extractFromCSV(text string) string {
	text = strings.TrimSpace(text)

	// Already looks like raw XML
	if strings.HasPrefix(text, "<") {
		return text
	}

	// Single quoted blob: "...xml..."
	if strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		unquoted := text[1 : len(text)-1]
		unquoted = strings.ReplaceAll(unquoted, `""`, `"`)
		if strings.Contains(unquoted, "<ShowPlanXML") {
			return unquoted
		}
	}

	// Multi-line CSV with headers
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	// Skip header
	if _, err := r.Read(); err == nil {
		if row, err := r.Read(); err == nil {
			for _, val := range row {
				val = strings.TrimSpace(val)
				if strings.HasPrefix(val, "<") {
					return val
				}
			}
		}
	}

	return text
}

// splitPlanDocuments splits multiple ShowPlanXML documents that may be
// concatenated (newline-separated, as produced by --plan-file).
func splitPlanDocuments(text string) []string {
	var chunks []string
	remaining := text
	for {
		idx := strings.Index(remaining, "<ShowPlanXML")
		if idx < 0 {
			break
		}
		remaining = remaining[idx:]
		// Find the closing tag
		end := strings.Index(remaining, "</ShowPlanXML>")
		if end < 0 {
			chunks = append(chunks, remaining)
			break
		}
		end += len("</ShowPlanXML>")
		chunks = append(chunks, remaining[:end])
		remaining = remaining[end:]
	}
	return chunks
}

// parseSinglePlan parses a single ShowPlanXML document using streaming XML parsing.
func parseSinglePlan(data []byte) (*ShowPlanXML, error) {
	plan := &ShowPlanXML{}
	decoder := xml.NewDecoder(bytes.NewReader(data))

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		if se.Name.Local == "StmtSimple" {
			stmt, err := parseStmtSimple(decoder, se)
			if err != nil {
				return nil, err
			}
			plan.Statements = append(plan.Statements, *stmt)
		}
	}
	return plan, nil
}

func parseStmtSimple(decoder *xml.Decoder, start xml.StartElement) (*StmtSimple, error) {
	stmt := &StmtSimple{
		Attrs: attrsToMap(start.Attr),
	}

	depth := 1
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "StatementSetOptions":
				stmt.SetOptions = attrsToMap(t.Attr)
			case "QueryPlan":
				qp, d, err := parseQueryPlan(decoder, t)
				if err != nil {
					return nil, err
				}
				stmt.QueryPlan = qp
				depth += d
			}
		case xml.EndElement:
			depth--
		}
	}
	return stmt, nil
}

func parseQueryPlan(decoder *xml.Decoder, start xml.StartElement) (*QueryPlan, int, error) {
	qp := &QueryPlan{
		Attrs: attrsToMap(start.Attr),
	}

	depth := 1
	// We track depth changes but return the accumulated change so the caller can adjust.
	// Since we consume until our own closing tag, we return 0 net depth.
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return nil, 0, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "MemoryGrantInfo":
				qp.MemoryGrant = attrsToMap(t.Attr)
			case "OptimizerHardwareDependentProperties":
				qp.HardwareProps = attrsToMap(t.Attr)
			case "Warnings":
				w, err := parseWarnings(decoder)
				if err != nil {
					return nil, 0, err
				}
				qp.Warnings = w
				depth-- // parseWarnings consumed up to and including </Warnings>
			case "QueryTimeStats":
				qp.TimeStats = attrsToMap(t.Attr)
			case "StatisticsInfo":
				qp.StatsRefCount++
			case "RelOp":
				op, err := parseRelOp(decoder, t)
				if err != nil {
					return nil, 0, err
				}
				qp.Root = op
				depth-- // parseRelOp consumed up to and including </RelOp>
			}
		case xml.EndElement:
			depth--
		}
	}
	return qp, 0, nil
}

func parseRelOp(decoder *xml.Decoder, start xml.StartElement) (*RelOp, error) {
	attrs := attrsToMap(start.Attr)
	op := &RelOp{
		NodeID:     attrInt(attrs, "NodeId"),
		PhysicalOp: attrs["PhysicalOp"],
		LogicalOp:  attrs["LogicalOp"],
		EstRows:    attrFloat(attrs, "EstimateRows"),
	}

	depth := 1
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "RunTimeCountersPerThread":
				a := attrsToMap(t.Attr)
				op.Threads = append(op.Threads, threadInfo{
					ActualRows:    attrInt64(a, "ActualRows"),
					ActualElapsed: attrInt64(a, "ActualElapsedms"),
				})
			case "Object":
				op.ObjectInfo = formatObjectInfo(attrsToMap(t.Attr))
			case "Warnings":
				w, err := parseWarnings(decoder)
				if err != nil {
					return nil, err
				}
				op.Warnings = w
				depth-- // parseWarnings consumed </Warnings>
			case "RelOp":
				child, err := parseRelOp(decoder, t)
				if err != nil {
					return nil, err
				}
				op.Children = append(op.Children, child)
				depth-- // child consumed its own </RelOp>
			}
		case xml.EndElement:
			depth--
		}
	}
	return op, nil
}

func parseWarnings(decoder *xml.Decoder) ([]xmlWarning, error) {
	var warnings []xmlWarning
	depth := 1
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			w := xmlWarning{
				Tag:   t.Name.Local,
				Attrs: attrsToMap(t.Attr),
			}
			// Read children of this warning element
			childDepth := 1
			for childDepth > 0 {
				ctok, cerr := decoder.Token()
				if cerr != nil {
					return nil, cerr
				}
				switch ct := ctok.(type) {
				case xml.StartElement:
					childDepth++
					cw := xmlWarning{
						Tag:   ct.Name.Local,
						Attrs: attrsToMap(ct.Attr),
					}
					w.Children = append(w.Children, cw)
				case xml.EndElement:
					childDepth--
				}
			}
			depth-- // consumed end of this warning element
			warnings = append(warnings, w)
		case xml.EndElement:
			depth--
		}
	}
	return warnings, nil
}

func formatObjectInfo(attrs map[string]string) string {
	table := stripBrackets(attrs["Table"])
	index := stripBrackets(attrs["Index"])
	if table == "" {
		return ""
	}
	if index != "" {
		return fmt.Sprintf("%s.%s", table, index)
	}
	return table
}

func stripBrackets(s string) string {
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return s[1 : len(s)-1]
	}
	return s
}

func attrsToMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[a.Name.Local] = a.Value
	}
	return m
}

func attrInt(m map[string]string, key string) int {
	v, _ := strconv.Atoi(m[key])
	return v
}

func attrInt64(m map[string]string, key string) int64 {
	v, _ := strconv.ParseInt(m[key], 10, 64)
	return v
}

func attrFloat(m map[string]string, key string) float64 {
	v, _ := strconv.ParseFloat(m[key], 64)
	return v
}
