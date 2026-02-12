// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package sqlcmd

import (
	"fmt"
	"strings"
	"unicode"
)

// ReadOnlyError is returned when a statement is rejected by read-only mode.
type ReadOnlyError struct {
	Keyword string
}

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("%sread-only mode rejected '%s' statement. Use --rw to allow writes.", ErrorPrefix, e.Keyword)
}

func (e *ReadOnlyError) IsSqlcmdErr() bool {
	return true
}

// CheckReadOnly scans query for destructive SQL statements and returns an error
// if any are found. It is a client-side safety net, not a security boundary.
//
// allowExec controls whether EXEC/EXECUTE statements are permitted.
func CheckReadOnly(query string, allowExec bool) error {
	s := &scanner{runes: []rune(query), end: len([]rune(query))}

	for {
		kw := s.nextStatementKeyword()
		if kw == "" {
			return nil
		}
		if err := classifyKeyword(s, kw, allowExec); err != nil {
			return err
		}
		// Skip past the rest of this statement to the next ';' boundary
		s.skipToStatementEnd()
	}
}

// Keyword classification tables.
var allowedKeywords = map[string]bool{
	"SELECT":    true,
	"SET":       true,
	"DECLARE":   true,
	"PRINT":     true,
	"USE":       true,
	"IF":        true,
	"ELSE":      true,
	"END":       true,
	"WHILE":     true,
	"RETURN":    true,
	"THROW":     true,
	"RAISERROR": true,
	"WAITFOR":   true,
	"BREAK":     true,
	"CONTINUE":  true,
	"GOTO":      true,
	"TRY":       true,
	"CATCH":     true,
}

var rejectedKeywords = map[string]bool{
	"INSERT":     true,
	"UPDATE":     true,
	"DELETE":     true,
	"DROP":       true,
	"ALTER":      true,
	"TRUNCATE":   true,
	"MERGE":      true,
	"GRANT":      true,
	"REVOKE":     true,
	"DENY":       true,
	"BULK":       true,
	"BACKUP":     true,
	"RESTORE":    true,
	"DBCC":       true,
	"SHUTDOWN":   true,
	"KILL":       true,
	"OPEN":       true,
	"CLOSE":      true,
	"DEALLOCATE": true,
	"FETCH":      true,
	"COMMIT":     true,
	"ROLLBACK":   true,
	"SAVE":       true,
}

func classifyKeyword(s *scanner, kw string, allowExec bool) error {
	upper := strings.ToUpper(kw)

	switch {
	case allowedKeywords[upper]:
		if upper == "SELECT" {
			return checkSelectInto(s)
		}
		return nil

	case upper == "BEGIN":
		return checkBeginTran(s)

	case upper == "CREATE":
		return checkCreateTemp(s)

	case upper == "WITH":
		return checkCTE(s)

	case upper == "EXEC" || upper == "EXECUTE":
		if allowExec {
			return nil
		}
		return &ReadOnlyError{Keyword: upper}

	case rejectedKeywords[upper]:
		return &ReadOnlyError{Keyword: upper}

	default:
		// Unknown keyword — reject for safety
		return &ReadOnlyError{Keyword: upper}
	}
}

// checkBeginTran peeks at the next keyword after BEGIN.
// "BEGIN TRAN" and "BEGIN TRANSACTION" are rejected; plain BEGIN (for blocks) is allowed.
func checkBeginTran(s *scanner) error {
	saved := s.pos
	next := s.nextKeyword()
	upper := strings.ToUpper(next)
	if upper == "TRAN" || upper == "TRANSACTION" {
		return &ReadOnlyError{Keyword: "BEGIN " + upper}
	}
	// Not a transaction — rewind and allow
	s.pos = saved
	return nil
}

// checkCreateTemp peeks ahead after CREATE. If it's "TABLE #..." or "TABLE ##...", allow it.
func checkCreateTemp(s *scanner) error {
	saved := s.pos
	next := strings.ToUpper(s.nextKeyword())
	if next == "TABLE" {
		s.skipWhitespace()
		if s.pos < s.end && s.runes[s.pos] == '#' {
			return nil
		}
	}
	s.pos = saved
	return &ReadOnlyError{Keyword: "CREATE"}
}

// checkSelectInto scans for INTO before FROM within the current statement.
// "SELECT ... INTO #tmp" is allowed; "SELECT ... INTO real_table" is rejected.
func checkSelectInto(s *scanner) error {
	saved := s.pos
	for {
		kw := s.nextKeyword()
		if kw == "" {
			break
		}
		upper := strings.ToUpper(kw)

		switch upper {
		case "FROM":
			// Reached FROM without encountering INTO — safe
			s.pos = saved
			return nil
		case "INTO":
			// Check what follows INTO
			s.skipWhitespace()
			if s.pos < s.end && s.runes[s.pos] == '#' {
				s.pos = saved
				return nil
			}
			s.pos = saved
			return &ReadOnlyError{Keyword: "SELECT INTO"}
		}
	}
	s.pos = saved
	return nil
}

// checkCTE handles WITH...AS(...) followed by the actual DML keyword.
// WITH...SELECT is safe; WITH...DELETE/UPDATE/INSERT/MERGE is not.
//
// CTE syntax: WITH name AS (...) [, name AS (...)]* SELECT/INSERT/UPDATE/DELETE/MERGE
// nextKeyword already skips parenthesized content, so we see: name, AS, name, AS, ..., then the DML keyword.
// We skip all identifiers until we find a known DML keyword.
func checkCTE(s *scanner) error {
	dmlKeywords := map[string]bool{
		"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
	}
	for {
		kw := s.nextKeyword()
		if kw == "" {
			return nil // end of input — nothing dangerous
		}

		upper := strings.ToUpper(kw)
		if !dmlKeywords[upper] {
			continue // skip CTE names, AS, column names, etc.
		}

		if upper == "SELECT" {
			return nil
		}
		return &ReadOnlyError{Keyword: upper}
	}
}

// scanner provides low-level token scanning for SQL text.
type scanner struct {
	runes []rune
	pos   int
	end   int
}

// nextStatementKeyword returns the first keyword at the start of the next statement.
// It skips whitespace, comments, and semicolons to find a statement-starting keyword.
func (s *scanner) nextStatementKeyword() string {
	for {
		s.skipWS()
		if s.pos >= s.end {
			return ""
		}

		// Skip statement separators
		if s.runes[s.pos] == ';' {
			s.pos++
			continue
		}

		kw := s.readWord()
		if kw == "" {
			// Not a word character — skip it
			s.pos++
			continue
		}

		return kw
	}
}

// nextKeyword returns the next keyword within a statement, skipping whitespace,
// comments, parenthesized content, quoted identifiers, and punctuation.
// Returns "" at end of input or statement boundary (';').
func (s *scanner) nextKeyword() string {
	for {
		s.skipWS()
		if s.pos >= s.end {
			return ""
		}

		c := s.runes[s.pos]

		// Statement boundary
		if c == ';' {
			return ""
		}

		// Skip parenthesized content (balanced)
		if c == '(' {
			s.skipBalancedParens()
			continue
		}

		// Skip quoted identifiers / strings
		if c == '\'' {
			s.skipSingleQuotedString()
			continue
		}
		if c == '"' {
			s.skipDoubleQuotedString()
			continue
		}
		if c == '[' {
			s.skipBracketIdentifier()
			continue
		}

		// Skip non-word characters (commas, operators, etc.)
		if !isWordChar(c) {
			s.pos++
			continue
		}

		return s.readWord()
	}
}

// skipToStatementEnd advances past all content until the next ';' or end of input,
// properly handling all quoting constructs.
func (s *scanner) skipToStatementEnd() {
	for s.pos < s.end {
		c := s.runes[s.pos]
		next := s.peek(1)

		switch {
		case c == ';':
			s.pos++
			return
		case c == '\'':
			s.skipSingleQuotedString()
		case c == '"':
			s.skipDoubleQuotedString()
		case c == '[':
			s.skipBracketIdentifier()
		case c == '-' && next == '-':
			s.skipLineComment()
		case c == '/' && next == '*':
			s.skipBlockComment()
		default:
			s.pos++
		}
	}
}

// readWord reads a contiguous keyword/identifier (unquoted).
func (s *scanner) readWord() string {
	start := s.pos
	for s.pos < s.end && isWordChar(s.runes[s.pos]) {
		s.pos++
	}
	if s.pos == start {
		return ""
	}
	return string(s.runes[start:s.pos])
}

func (s *scanner) skipWhitespace() {
	for s.pos < s.end && unicode.IsSpace(s.runes[s.pos]) {
		s.pos++
	}
}

// skipWS skips whitespace and comments (but not quoted strings).
func (s *scanner) skipWS() {
	for s.pos < s.end {
		c := s.runes[s.pos]

		if unicode.IsSpace(c) {
			s.pos++
			continue
		}

		next := s.peek(1)

		if c == '-' && next == '-' {
			s.skipLineComment()
			continue
		}

		if c == '/' && next == '*' {
			s.skipBlockComment()
			continue
		}

		break
	}
}

func (s *scanner) skipLineComment() {
	s.pos += 2
	for s.pos < s.end && s.runes[s.pos] != '\n' {
		s.pos++
	}
	if s.pos < s.end {
		s.pos++ // skip '\n'
	}
}

func (s *scanner) skipBlockComment() {
	s.pos += 2
	depth := 1
	for s.pos < s.end && depth > 0 {
		cc := s.runes[s.pos]
		nn := s.peek(1)
		if cc == '/' && nn == '*' {
			depth++
			s.pos += 2
		} else if cc == '*' && nn == '/' {
			depth--
			s.pos += 2
		} else {
			s.pos++
		}
	}
}

func (s *scanner) skipSingleQuotedString() {
	s.pos++ // skip opening '
	for s.pos < s.end {
		if s.runes[s.pos] == '\'' {
			s.pos++
			if s.pos < s.end && s.runes[s.pos] == '\'' {
				s.pos++ // escaped ''
				continue
			}
			return
		}
		s.pos++
	}
}

func (s *scanner) skipDoubleQuotedString() {
	s.pos++ // skip opening "
	for s.pos < s.end {
		if s.runes[s.pos] == '"' {
			s.pos++
			if s.pos < s.end && s.runes[s.pos] == '"' {
				s.pos++ // escaped ""
				continue
			}
			return
		}
		s.pos++
	}
}

func (s *scanner) skipBracketIdentifier() {
	s.pos++ // skip opening [
	for s.pos < s.end {
		if s.runes[s.pos] == ']' {
			s.pos++
			if s.pos < s.end && s.runes[s.pos] == ']' {
				s.pos++ // escaped ]]
				continue
			}
			return
		}
		s.pos++
	}
}

func (s *scanner) skipBalancedParens() {
	depth := 0
	for s.pos < s.end {
		c := s.runes[s.pos]
		next := s.peek(1)

		switch {
		case c == '(':
			depth++
			s.pos++
		case c == ')':
			depth--
			s.pos++
			if depth == 0 {
				return
			}
		case c == '\'':
			s.skipSingleQuotedString()
		case c == '"':
			s.skipDoubleQuotedString()
		case c == '[':
			s.skipBracketIdentifier()
		case c == '-' && next == '-':
			s.skipLineComment()
		case c == '/' && next == '*':
			s.skipBlockComment()
		default:
			s.pos++
		}
	}
}

func (s *scanner) peek(offset int) rune {
	idx := s.pos + offset
	if idx < s.end {
		return s.runes[idx]
	}
	return 0
}

func isWordChar(c rune) bool {
	return c == '_' || c == '#' || c == '@' || unicode.IsLetter(c) || unicode.IsDigit(c)
}
