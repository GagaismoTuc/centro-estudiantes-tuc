package roster

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxRows is the maximum number of data rows in one import.
const MaxRows = 5000

const maxNameLen = 200

// ErrTooManyRows is returned when the file has more than MaxRows data rows.
var ErrTooManyRows = errors.New("roster: too many rows")

// RowError is one rejected line; Line is 1-based and the header is line 1.
type RowError struct {
	Line   int
	Reason string
}

// InvalidRowsError lists every rejected line of an import; nothing was written.
type InvalidRowsError struct{ Rows []RowError }

func (e *InvalidRowsError) Error() string { return fmt.Sprintf("roster: %d invalid rows", len(e.Rows)) }

type entry struct{ name, doc string }

// dniPattern is a DNI of 7 to 9 digits, either plain or with dots in every thousands position
// (40.111.222, 9.876.543).
var dniPattern = regexp.MustCompile(`^(?:[0-9]{7,9}|[0-9]{1,3}\.[0-9]{3}\.[0-9]{3})$`)

// invisible reports format characters (Unicode category Cf): bidi overrides and isolates, zero-width
// characters and the like, which can make a name display differently from what is stored.
func invisible(r rune) bool { return unicode.Is(unicode.Cf, r) }

// parseCSV validates a whole roster file. The header must be full_name,document_number; the
// delimiter is a comma, or a semicolon when the header line has more semicolons than commas (what
// Excel writes with an Argentine locale).
func parseCSV(data []byte) ([]entry, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Excel writes a BOM
	r := csv.NewReader(bytes.NewReader(data))
	first, _, _ := bytes.Cut(data, []byte("\n"))
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil || len(header) != 2 ||
		!strings.EqualFold(strings.TrimSpace(header[0]), "full_name") ||
		!strings.EqualFold(strings.TrimSpace(header[1]), "document_number") {
		return nil, &InvalidRowsError{[]RowError{{1, "header must be full_name,document_number"}}}
	}
	var out []entry
	var bad []RowError
	seen := map[string]int{}
	for n := 1; ; n++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if n > MaxRows {
			return nil, ErrTooManyRows
		}
		if err != nil {
			line := 0
			if pe := (*csv.ParseError)(nil); errors.As(err, &pe) {
				line = pe.StartLine
			}
			bad = append(bad, RowError{line, "malformed CSV"})
			break
		}
		line, _ := r.FieldPos(0)
		reason := ""
		var e entry
		if len(rec) != 2 {
			reason = "expected 2 fields"
		} else {
			doc := strings.TrimSpace(rec[1])
			e = entry{strings.TrimSpace(rec[0]), strings.ReplaceAll(doc, ".", "")}
			switch {
			case !utf8.ValidString(rec[0]) || !utf8.ValidString(rec[1]):
				reason = "file must be saved as CSV UTF-8"
			case e.name == "":
				reason = "full_name is required"
			case len([]rune(e.name)) > maxNameLen:
				reason = fmt.Sprintf("full_name must have at most %d characters", maxNameLen)
			case strings.ContainsFunc(e.name, unicode.IsControl):
				reason = "full_name must be a single line of text"
			case strings.ContainsFunc(e.name, invisible):
				reason = "full_name contains invisible characters"
			case !dniPattern.MatchString(doc):
				reason = "document_number must have 7 to 9 digits"
			case seen[e.doc] != 0:
				reason = fmt.Sprintf("duplicate document_number (line %d)", seen[e.doc])
			}
		}
		if reason != "" {
			bad = append(bad, RowError{line, reason})
			continue
		}
		seen[e.doc] = line
		out = append(out, e)
	}
	if len(bad) > 0 {
		return nil, &InvalidRowsError{bad}
	}
	return out, nil
}
