package docx_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/docx"
)

func TestFillSplitFieldsAndRepeatedRows(t *testing.T) {
	template, err := os.ReadFile("testdata/word-template.docx")
	if err != nil {
		t.Fatal(err)
	}

	out, err := docx.Fill(template, map[string]string{"saldo_final": "-3000"}, map[string][]map[string]string{
		"entries": {{"description": `A < B & "C" {{saldo_final}}`, "amount": "2000"}, {"description": "Rent", "amount": "-15000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	var document *zip.File
	for _, file := range zr.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		t.Fatal("missing word/document.xml")
	}
	r, err := document.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var text strings.Builder
	rows := 0
	inText := false
	for {
		tok, e := decoder.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatalf("invalid XML: %v", e)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if v.Name.Local == "tr" {
				rows++
			}
			if v.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			if v.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				text.Write(v)
			}
		}
	}
	if got := text.String(); got != `-3000A < B & "C" {{saldo_final}}2000Rent-15000` {
		t.Fatalf("text = %q", got)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2", rows)
	}
	if !strings.Contains(string(raw), "&lt;") || !strings.Contains(string(raw), "&amp;") || !strings.Contains(string(raw), "&#34;") {
		t.Fatalf("description not escaped: %s", raw)
	}
}
