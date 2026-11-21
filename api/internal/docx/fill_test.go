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

func TestFillLiteralRowMarkersInFields(t *testing.T) {
	for _, value := range []string{"Nota {{#anexo}}", "Ver {{#anexo}}ok{{/anexo}}"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create("word/document.xml")
		_, _ = io.WriteString(w, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl><w:tr><w:tc><w:p><w:r><w:t>{{asunto}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`)
		_ = z.Close()
		out, err := docx.Fill(b.Bytes(), map[string]string{"asunto": value}, nil)
		if err != nil {
			t.Errorf("%q: %v", value, err)
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
		if err != nil {
			t.Fatal(err)
		}
		r, err := zr.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), value) || strings.Count(string(raw), "<w:tr>") != 1 {
			t.Errorf("%q: %s", value, raw)
		}
	}
}

func TestFillRepeatedParagraphs(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("word/document.xml")
	_, _ = io.WriteString(w, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{#entries}}{{description}}{{/entries}}</w:t></w:r></w:p></w:body></w:document>`)
	_ = z.Close()
	out, err := docx.Fill(b.Bytes(), nil, map[string][]map[string]string{"entries": {{"description": "One"}, {"description": "Two"}}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "<w:p>") != 2 || !strings.Contains(string(raw), "One") || !strings.Contains(string(raw), "Two") {
		t.Fatalf("unexpected output: %s", raw)
	}
}

func TestFillIgnoresEmptyRowName(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("word/document.xml")
	_, _ = io.WriteString(w, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{name}}</w:t></w:r></w:p></w:body></w:document>`)
	_ = z.Close()
	out, err := docx.Fill(b.Bytes(), map[string]string{"name": "original"}, map[string][]map[string]string{"": {{"name": "wrong"}}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "<w:p>") != 1 || !strings.Contains(string(raw), "original") || strings.Contains(string(raw), "wrong") {
		t.Fatalf("unexpected output: %s", raw)
	}
}

func TestFillPreservesBoundarySpacesInRuns(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("word/document.xml")
	_, _ = io.WriteString(w, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{name}} - {{count}}</w:t></w:r><w:r><w:t xml:space="preserve"> existing </w:t></w:r></w:p></w:body></w:document>`)
	_ = z.Close()
	out, err := docx.Fill(b.Bytes(), map[string]string{"name": "Cuotas", "count": "ingreso "}, nil)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `<w:t xml:space="preserve">Cuotas - ingreso </w:t>`) {
		t.Fatalf("boundary spaces not preserved: %s", raw)
	}
}
