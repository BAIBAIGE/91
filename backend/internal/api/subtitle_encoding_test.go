package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestNormalizeSubtitleUTF8(t *testing.T) {
	const subtitle = "1\n00:00:00,000 --> 00:00:01,000\n中文字幕𠀀\n"

	gbkText := "1\n00:00:00,000 --> 00:00:01,000\n中文字幕\n"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(gbkText))
	if err != nil {
		t.Fatalf("encode GBK fixture: %v", err)
	}
	gb18030, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(subtitle))
	if err != nil {
		t.Fatalf("encode GB18030 fixture: %v", err)
	}

	tests := []struct {
		name        string
		data        []byte
		contentType string
		want        string
	}{
		{name: "UTF-8", data: []byte(subtitle), want: subtitle},
		{name: "UTF-8 BOM", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(subtitle)...), want: subtitle},
		{name: "GBK", data: gbk, want: gbkText},
		{name: "GB18030", data: gb18030, want: subtitle},
		{name: "UTF-16LE BOM", data: encodeUTF16Fixture(subtitle, binary.LittleEndian, true), want: subtitle},
		{name: "UTF-16BE BOM", data: encodeUTF16Fixture(subtitle, binary.BigEndian, true), want: subtitle},
		{
			name:        "UTF-16LE header",
			data:        encodeUTF16Fixture(subtitle, binary.LittleEndian, false),
			contentType: "text/plain; charset=utf-16le",
			want:        subtitle,
		},
		{
			name:        "UTF-16BE header",
			data:        encodeUTF16Fixture(subtitle, binary.BigEndian, false),
			contentType: "text/plain; charset=UTF-16BE",
			want:        subtitle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSubtitleUTF8(tt.data, tt.contentType, maxSubtitleBytes)
			if err != nil {
				t.Fatalf("normalize subtitle: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("normalized subtitle = %q, want %q", got, tt.want)
			}
			if !utf8.Valid(got) {
				t.Fatal("normalized subtitle is not valid UTF-8")
			}
		})
	}
}

func TestNormalizeSubtitleUTF8RejectsInvalidEncoding(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		contentType string
	}{
		{name: "invalid multibyte sequence", data: []byte{0x81, 0x30, 0x20, 0x30}},
		{name: "UTF-32LE", data: []byte{0xff, 0xfe, 0x00, 0x00, 0x61, 0x00, 0x00, 0x00}},
		{name: "UTF-16 odd bytes", data: []byte{0xff, 0xfe, 0x61}},
		{name: "UTF-16 unpaired surrogate", data: []byte{0xff, 0xfe, 0x00, 0xd8}},
		{name: "UTF-16 without byte order", data: []byte{0x61, 0x00}, contentType: "text/plain; charset=utf-16"},
		{name: "embedded NUL", data: []byte{'a', 0, 'b'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeSubtitleUTF8(tt.data, tt.contentType, maxSubtitleBytes)
			if !errors.Is(err, errSubtitleEncoding) {
				t.Fatalf("error = %v, want errSubtitleEncoding", err)
			}
		})
	}
}

func TestNormalizeSubtitleUTF8ChecksConvertedSize(t *testing.T) {
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中文"))
	if err != nil {
		t.Fatalf("encode GBK fixture: %v", err)
	}
	if len(encoded) != 4 {
		t.Fatalf("GBK fixture length = %d, want 4", len(encoded))
	}

	_, err = normalizeSubtitleUTF8(encoded, "", 5)
	if !errors.Is(err, errSubtitleTooLarge) {
		t.Fatalf("error = %v, want errSubtitleTooLarge after UTF-8 expansion", err)
	}
}

func encodeUTF16Fixture(value string, order binary.ByteOrder, withBOM bool) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2+2)
	if withBOM {
		if order == binary.LittleEndian {
			out = append(out, 0xff, 0xfe)
		} else {
			out = append(out, 0xfe, 0xff)
		}
	}
	for _, unit := range units {
		var encoded [2]byte
		order.PutUint16(encoded[:], unit)
		out = append(out, encoded[:]...)
	}
	return out
}

func TestDecodeSimplifiedChinesePreservesSubtitleStructure(t *testing.T) {
	want := []byte("[Script Info]\r\nTitle: 中文\r\n[Events]\r\nDialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,你好\r\n")
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes(want)
	if err != nil {
		t.Fatalf("encode ASS fixture: %v", err)
	}

	got, err := decodeSimplifiedChinese(encoded)
	if err != nil {
		t.Fatalf("decode ASS fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded ASS fixture = %q, want %q", got, want)
	}
}
