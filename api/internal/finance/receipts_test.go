package finance

import (
	"bytes"
	"errors"
	"testing"
)

func TestDetectReceiptType(t *testing.T) {
	t.Run("R2 accepts JPG, PNG, and PDF by magic bytes", func(t *testing.T) {
		cases := []struct {
			name   string
			data   []byte
			wantCT string
		}{
			{"jpg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, "image/jpeg"},
			{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}, "image/png"},
			{"pdf", []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"), "application/pdf"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ct, err := DetectReceiptType(tc.data)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ct != tc.wantCT {
					t.Fatalf("got %q, want %q", ct, tc.wantCT)
				}
			})
		}
	})

	t.Run("R2 rejects PE executable, ELF, GIF, DOCX, and truncated header", func(t *testing.T) {
		cases := []struct {
			name string
			data []byte
		}{
			{"pe_exe", []byte("MZ\x90\x00\x03\x00\x00\x00")},
			{"elf_exe", []byte("\x7fELF\x02\x01\x01\x00")},
			{"gif", []byte("GIF89a\x01\x00\x01\x00")},
			{"docx_zip", []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00}},
			{"truncated_jpg", []byte{0xFF, 0xD8}},
			{"truncated_png", []byte{0x89, 0x50, 0x4E, 0x47}},
			{"truncated_pdf", []byte("%PDF")},
			{"plain_text", []byte("hello world")},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := DetectReceiptType(tc.data)
				if !errors.Is(err, ErrUnsupportedMediaType) {
					t.Fatalf("got %v, want ErrUnsupportedMediaType", err)
				}
			})
		}
	})

	t.Run("R3 empty and size boundary", func(t *testing.T) {
		if _, err := DetectReceiptType(nil); !errors.Is(err, ErrEmptyFile) {
			t.Fatalf("empty nil: got %v, want ErrEmptyFile", err)
		}
		if _, err := DetectReceiptType([]byte{}); !errors.Is(err, ErrEmptyFile) {
			t.Fatalf("empty slice: got %v, want ErrEmptyFile", err)
		}

		exact := make([]byte, MaxReceiptBytes)
		copy(exact, []byte("%PDF-1.4\n"))
		ct, err := DetectReceiptType(exact)
		if err != nil || ct != "application/pdf" {
			t.Fatalf("exact max (%d bytes): got (%q, %v)", len(exact), ct, err)
		}

		over := make([]byte, MaxReceiptBytes+1)
		copy(over, bytes.Repeat([]byte("a"), 16))
		copy(over, []byte("%PDF-1.4\n"))
		if _, err := DetectReceiptType(over); !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("over max (%d bytes): got %v, want ErrFileTooLarge", len(over), err)
		}
	})
}
