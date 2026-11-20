package httpapi_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// shortcut: Test-only PDF text extraction for the pinned converter; upgrade if reports need
// general PDF parsing or layout validation.
func converterText(pdf []byte) (string, error) {
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("%%EOF")) {
		return "", fmt.Errorf("invalid PDF framing")
	}
	objects := map[string][]byte{}
	for _, m := range regexp.MustCompile(`(?s)(\d+)\s+0\s+obj\b(.*?)endobj`).FindAllSubmatch(pdf, -1) {
		objects[string(m[1])] = m[2]
	}
	streams := map[string][]byte{}
	for id, object := range objects {
		marker := regexp.MustCompile(`stream\r?\n`).FindIndex(object)
		if marker == nil || !bytes.Contains(object[:marker[0]], []byte("/FlateDecode")) {
			continue
		}
		end := bytes.LastIndex(object, []byte("endstream"))
		if end < marker[1] {
			continue
		}
		reader, err := zlib.NewReader(bytes.NewReader(object[marker[1]:end]))
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return "", err
		}
		streams[id] = data
	}
	mapPattern := regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	pairPattern := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	cmaps := map[string]map[byte]string{}
	for id, stream := range streams {
		for _, block := range mapPattern.FindAllSubmatch(stream, -1) {
			if cmaps[id] == nil {
				cmaps[id] = map[byte]string{}
			}
			for _, pair := range pairPattern.FindAllSubmatch(block[1], -1) {
				code, err := hex.DecodeString(string(pair[1]))
				if err != nil || len(code) != 1 {
					return "", fmt.Errorf("unsupported glyph code")
				}
				value, err := hex.DecodeString(string(pair[2]))
				if err != nil || len(value)%2 != 0 {
					return "", fmt.Errorf("invalid Unicode map")
				}
				words := make([]uint16, len(value)/2)
				for i := range words {
					words[i] = binary.BigEndian.Uint16(value[2*i:])
				}
				cmaps[id][code[0]] = string(utf16.Decode(words))
			}
		}
	}
	unicodeRef := regexp.MustCompile(`/ToUnicode\s+(\d+)\s+0\s+R`)
	fontMaps := map[string]map[byte]string{}
	for id, object := range objects {
		if ref := unicodeRef.FindSubmatch(object); ref != nil {
			fontMaps[id] = cmaps[string(ref[1])]
		}
	}
	fontRef := regexp.MustCompile(`/F(\d+)\s+(\d+)\s+0\s+R`)
	fonts := map[string]map[byte]string{}
	for _, object := range objects {
		for _, ref := range fontRef.FindAllSubmatch(object, -1) {
			if fontMaps[string(ref[2])] != nil {
				fonts[string(ref[1])] = fontMaps[string(ref[2])]
			}
		}
	}
	blocks := regexp.MustCompile(`(?s)\bBT\b(.*?)\bET\b`)
	font := regexp.MustCompile(`/F(\d+)\s+[0-9.]+\s+Tf`)
	hexText := regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
	var text strings.Builder
	for _, stream := range streams {
		for _, block := range blocks.FindAllSubmatch(stream, -1) {
			f := font.FindSubmatch(block[1])
			if f == nil {
				continue
			}
			mapping := fonts[string(f[1])]
			if mapping == nil {
				return "", fmt.Errorf("missing font map %s", f[1])
			}
			for _, encoded := range hexText.FindAllSubmatch(block[1], -1) {
				glyphs, err := hex.DecodeString(string(encoded[1]))
				if err != nil {
					return "", err
				}
				for _, glyph := range glyphs {
					value, ok := mapping[glyph]
					if !ok {
						return "", fmt.Errorf("missing glyph %s", strconv.Itoa(int(glyph)))
					}
					text.WriteString(value)
				}
			}
			text.WriteByte('\n')
		}
	}
	return text.String(), nil
}
