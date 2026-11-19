// Package docx fills fields and repeated table rows in Word documents.
package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const wordNamespace = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

var fieldPattern = regexp.MustCompile(`\{\{([#/]?[A-Za-z0-9_]+)\}\}`)

type span struct {
	start, end int
	text       string
}

// Fill replaces {{name}} fields and repeats a table row delimited by
// {{#name}} and {{/name}} for each value in rows[name]. Missing values are empty.
func Fill(template []byte, fields map[string]string, rows map[string][]map[string]string) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if isStory(file.Name) {
			var repeated []span
			repeated, err = repeatedRows(data)
			if err == nil {
				data, err = fillParagraphs(data, fields, repeated)
			}
			if err == nil {
				data, err = repeatRows(data, fields, rows)
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file.Name, err)
			}
		}
		header := file.FileHeader
		entry, err := writer.CreateHeader(&header)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func isStory(name string) bool {
	return name == "word/document.xml" || strings.HasPrefix(name, "word/header") && strings.HasSuffix(name, ".xml") || strings.HasPrefix(name, "word/footer") && strings.HasSuffix(name, ".xml")
}

// elements returns the byte ranges of matching Word elements in the original XML.
func elements(data []byte, local string) ([]span, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var result []span
	var stack []int
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Space == wordNamespace && v.Name.Local == local {
				stack = append(stack, before)
			}
		case xml.EndElement:
			if v.Name.Space == wordNamespace && v.Name.Local == local {
				start := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				result = append(result, span{start: start, end: int(decoder.InputOffset())})
			}
		}
	}
}

// textSpans keeps original byte positions while decoding XML entities.
func textSpans(data []byte) ([]span, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var result []span
	inText := false
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Space == wordNamespace && v.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			if v.Name.Space == wordNamespace && v.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				result = append(result, span{before, int(decoder.InputOffset()), string(v)})
			}
		}
	}
}

func repeatedRows(data []byte) ([]span, error) {
	ranges, err := elements(data, "tr")
	if err != nil {
		return nil, err
	}
	var result []span
	for _, r := range ranges {
		texts, err := textSpans(wrap(data[r.start:r.end]))
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		for _, t := range texts {
			text.WriteString(t.text)
		}
		matches := fieldPattern.FindAllStringSubmatch(text.String(), -1)
		name := ""
		for _, m := range matches {
			if strings.HasPrefix(m[1], "#") {
				name = m[1][1:]
				break
			}
		}
		if name == "" {
			continue
		}
		if !strings.Contains(text.String(), "{{/"+name+"}}") {
			return nil, fmt.Errorf("unclosed row %s", name)
		}
		r.text = name
		result = append(result, r)
	}
	return result, nil
}

func repeatRows(data []byte, fields map[string]string, rows map[string][]map[string]string) ([]byte, error) {
	ranges, err := repeatedRows(data)
	if err != nil {
		return nil, err
	}
	for i := len(ranges) - 1; i >= 0; i-- {
		r := ranges[i]
		wrapped := wrap(data[r.start:r.end])
		var expanded bytes.Buffer
		for _, values := range rows[r.text] {
			merged := make(map[string]string, len(fields)+len(values))
			for k, v := range fields {
				merged[k] = v
			}
			for k, v := range values {
				merged[k] = v
			}
			filled, err := fillParagraphs(wrapped, merged, nil)
			if err != nil {
				return nil, err
			}
			expanded.Write(unwrap(filled))
		}
		data = append(append(append([]byte{}, data[:r.start]...), expanded.Bytes()...), data[r.end:]...)
	}
	return data, nil
}

const wrapper = `<root xmlns:w="` + wordNamespace + `">`

func wrap(data []byte) []byte   { return append(append([]byte(wrapper), data...), []byte("</root>")...) }
func unwrap(data []byte) []byte { return data[len(wrapper) : len(data)-len("</root>")] }

func fillParagraphs(data []byte, fields map[string]string, excluded []span) ([]byte, error) {
	paragraphs, err := elements(data, "p")
	if err != nil {
		return nil, err
	}
	for i := len(paragraphs) - 1; i >= 0; i-- {
		p := paragraphs[i]
		skip := false
		for _, r := range excluded {
			if p.start >= r.start && p.end <= r.end {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		fragment := wrap(data[p.start:p.end])
		spans, err := textSpans(fragment)
		if err != nil {
			return nil, err
		}
		var full strings.Builder
		for _, s := range spans {
			full.WriteString(s.text)
		}
		text := full.String()
		matches := fieldPattern.FindAllStringSubmatchIndex(text, -1)
		pieces := make([]strings.Builder, len(spans))
		starts := make([]int, len(spans)+1)
		for n, s := range spans {
			starts[n+1] = starts[n] + len(s.text)
		}
		copyText := func(from, to int) {
			for n := range spans {
				a, b := max(from, starts[n]), min(to, starts[n+1])
				if a < b {
					pieces[n].WriteString(text[a:b])
				}
			}
		}
		pos := 0
		for _, m := range matches {
			copyText(pos, m[0])
			for n := range spans {
				if starts[n] <= m[0] && m[0] < starts[n+1] {
					pieces[n].WriteString(fields[text[m[2]:m[3]]])
					break
				}
			}
			pos = m[1]
		}
		copyText(pos, len(text))
		for n := len(spans) - 1; n >= 0; n-- {
			var escaped bytes.Buffer
			if err := xml.EscapeText(&escaped, []byte(pieces[n].String())); err != nil {
				return nil, err
			}
			s := spans[n]
			fragment = append(append(append([]byte{}, fragment[:s.start]...), escaped.Bytes()...), fragment[s.end:]...)
		}
		data = append(append(append([]byte{}, data[:p.start]...), unwrap(fragment)...), data[p.end:]...)
	}
	return data, nil
}
