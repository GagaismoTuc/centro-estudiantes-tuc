package roster

import (
	"errors"
	"slices"
	"testing"
)

// Every rejected line carries a machine-readable code so the web can explain the actual problem.
func TestParseCSVRowErrorCodes(t *testing.T) {
	const header = "full_name,document_number\n"
	for _, tc := range []struct {
		name, body string
		want       []RowError
	}{
		{"header", "Ana,30111222\n", []RowError{{1, CodeHeaderInvalid, "header must be full_name,document_number", 0}}},
		{"empty file", "", []RowError{{1, CodeHeaderInvalid, "header must be full_name,document_number", 0}}},
		{"field count", header + "Ana,30111222,extra\n", []RowError{{2, CodeFieldCount, "expected 2 fields", 0}}},
		{"encoding", header + "Mu\xf1oz,30111222\n", []RowError{{2, CodeEncodingInvalid, "file must be saved as CSV UTF-8", 0}}},
		{"name required", header + " ,30111222\n", []RowError{{2, CodeFullNameRequired, "full_name is required", 0}}},
		{"name too long", header + longName() + ",30111222\n",
			[]RowError{{2, CodeFullNameTooLong, "full_name must have at most 200 characters", 0}}},
		{"name control", header + "\"Ana\nGómez\",30111222\n",
			[]RowError{{2, CodeFullNameInvalid, "full_name must be a single line of text", 0}}},
		{"name invisible", header + "Ana\u202eGómez,30111222\n",
			[]RowError{{2, CodeFullNameInvisible, "full_name contains invisible characters", 0}}},
		{"dni spaces", header + "Ana,42 345 678\n",
			[]RowError{{2, CodeDocumentNumberSpaces, "document_number must not contain spaces", 0}}},
		{"dni letters", header + "Ana,40.111.22a\n",
			[]RowError{{2, CodeDocumentNumberFormat, "document_number must have only digits, with or without dots", 0}}},
		{"dni misplaced dots", header + "Ana,4011.1222\n",
			[]RowError{{2, CodeDocumentNumberFormat, "document_number must have only digits, with or without dots", 0}}},
		{"dni short", header + "Ana,123\n",
			[]RowError{{2, CodeDocumentNumberLength, "document_number must have 7 to 9 digits", 0}}},
		{"dni long", header + "Ana,1234567890\n",
			[]RowError{{2, CodeDocumentNumberLength, "document_number must have 7 to 9 digits", 0}}},
		{"dni short with dots", header + "Ana,40.111\n",
			[]RowError{{2, CodeDocumentNumberLength, "document_number must have 7 to 9 digits", 0}}},
		{"dni required", header + "Ana, \n",
			[]RowError{{2, CodeDocumentNumberRequired, "document_number is required", 0}}},
		{"dni duplicate", header + "Ana,30111222\nBeto,30.111.222\n",
			[]RowError{{3, CodeDocumentNumberDuplicate, "duplicate document_number (line 2)", 2}}},
		{"malformed", header + "Ana,30111222\n\"Beto,40111222\n", []RowError{{3, CodeCSVMalformed, "malformed CSV", 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCSV([]byte(tc.body))
			var invalid *InvalidRowsError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *InvalidRowsError", err)
			}
			if !slices.Equal(invalid.Rows, tc.want) {
				t.Fatalf("rows = %+v, want %+v", invalid.Rows, tc.want)
			}
		})
	}
}

// Valid DNI shapes keep working: plain or dotted in every thousands position.
func TestParseCSVAcceptsValidDocumentNumbers(t *testing.T) {
	got, err := parseCSV([]byte("full_name,document_number\nAna,40.111.222\nBeto,9.876.543\nCarla,1234567\nDani,123456789\n"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []entry{{"Ana", "40111222"}, {"Beto", "9876543"}, {"Carla", "1234567"}, {"Dani", "123456789"}}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
}

func longName() string {
	b := make([]rune, 201)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
