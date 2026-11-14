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

// RowError is one rejected line; Line is 1-based and the header is line 1. Code is stable and
// machine-readable (the web explains it); Reason is the same in English. DuplicateOf is the line
// that already has the document number (only for CodeDocumentNumberDuplicate).
type RowError struct {
	Line        int
	Code        string
	Reason      string
	DuplicateOf int
}

// Row error codes, mirrored by the RowError.code enum in openapi/openapi.yaml.
const (
	CodeHeaderInvalid           = "header_invalid"
	CodeFieldCount              = "field_count"
	CodeEncodingInvalid         = "encoding_invalid"
	CodeCSVMalformed            = "csv_malformed"
	CodeFullNameRequired        = "full_name_required"
	CodeFullNameTooLong         = "full_name_too_long"
	CodeFullNameInvalid         = "full_name_invalid"
	CodeFullNameInvisible       = "full_name_invisible"
	CodeDocumentNumberRequired  = "document_number_required"
	CodeDocumentNumberSpaces    = "document_number_spaces"
	CodeDocumentNumberFormat    = "document_number_format"
	CodeDocumentNumberLength    = "document_number_length"
	CodeDocumentNumberDuplicate = "document_number_duplicate"
)

// RowErrorCodes lists every code parseCSV can report.
var RowErrorCodes = []string{
	CodeHeaderInvalid, CodeFieldCount, CodeEncodingInvalid, CodeCSVMalformed,
	CodeFullNameRequired, CodeFullNameTooLong, CodeFullNameInvalid, CodeFullNameInvisible,
	CodeDocumentNumberRequired, CodeDocumentNumberSpaces, CodeDocumentNumberFormat,
	CodeDocumentNumberLength, CodeDocumentNumberDuplicate,
}

// InvalidRowsError lists every rejected line of an import; nothing was written.
type InvalidRowsError struct{ Rows []RowError }

func (e *InvalidRowsError) Error() string { return fmt.Sprintf("roster: %d invalid rows", len(e.Rows)) }

type entry struct{ name, doc string }

// dniPattern is a DNI of 7 to 9 digits, either plain or with dots in every thousands position
// (40.111.222, 9.876.543).
var dniPattern = regexp.MustCompile(`^(?:[0-9]{7,9}|[0-9]{1,3}\.[0-9]{3}\.[0-9]{3})$`)

// dottedPattern is digits with dots in every thousands position, whatever the length.
var dottedPattern = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]{3})*$`)

// checkDocument names the first problem of a trimmed document number, or returns "" when it is valid.
func checkDocument(doc string) (code, reason string) {
	digits := strings.ReplaceAll(doc, ".", "")
	switch {
	case dniPattern.MatchString(doc):
		return "", ""
	case doc == "":
		return CodeDocumentNumberRequired, "document_number is required"
	case strings.ContainsFunc(doc, unicode.IsSpace):
		return CodeDocumentNumberSpaces, "document_number must not contain spaces"
	case strings.ContainsFunc(digits, func(r rune) bool { return r < '0' || r > '9' }),
		strings.Contains(doc, ".") && !dottedPattern.MatchString(doc):
		return CodeDocumentNumberFormat, "document_number must have only digits, with or without dots"
	default:
		return CodeDocumentNumberLength, "document_number must have 7 to 9 digits"
	}
}

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
		return nil, &InvalidRowsError{[]RowError{{Line: 1, Code: CodeHeaderInvalid,
			Reason: "header must be full_name,document_number"}}}
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
			bad = append(bad, RowError{Line: line, Code: CodeCSVMalformed, Reason: "malformed CSV"})
			break
		}
		line, _ := r.FieldPos(0)
		rowErr := RowError{Line: line}
		var e entry
		if len(rec) != 2 {
			rowErr.Code, rowErr.Reason = CodeFieldCount, "expected 2 fields"
		} else {
			doc := strings.TrimSpace(rec[1])
			e = entry{strings.TrimSpace(rec[0]), strings.ReplaceAll(doc, ".", "")}
			switch {
			case !utf8.ValidString(rec[0]) || !utf8.ValidString(rec[1]):
				rowErr.Code, rowErr.Reason = CodeEncodingInvalid, "file must be saved as CSV UTF-8"
			case e.name == "":
				rowErr.Code, rowErr.Reason = CodeFullNameRequired, "full_name is required"
			case len([]rune(e.name)) > maxNameLen:
				rowErr.Code, rowErr.Reason = CodeFullNameTooLong, fmt.Sprintf("full_name must have at most %d characters", maxNameLen)
			case strings.ContainsFunc(e.name, unicode.IsControl):
				rowErr.Code, rowErr.Reason = CodeFullNameInvalid, "full_name must be a single line of text"
			case strings.ContainsFunc(e.name, invisible):
				rowErr.Code, rowErr.Reason = CodeFullNameInvisible, "full_name contains invisible characters"
			default:
				rowErr.Code, rowErr.Reason = checkDocument(doc)
				if rowErr.Code == "" && seen[e.doc] != 0 {
					rowErr.Code, rowErr.DuplicateOf = CodeDocumentNumberDuplicate, seen[e.doc]
					rowErr.Reason = fmt.Sprintf("duplicate document_number (line %d)", seen[e.doc])
				}
			}
		}
		if rowErr.Code != "" {
			bad = append(bad, rowErr)
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
