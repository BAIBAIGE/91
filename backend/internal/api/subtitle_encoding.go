package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"mime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var (
	errSubtitleEncoding = errors.New("subtitle encoding is unsupported or invalid")
	errSubtitleTooLarge = errors.New("subtitle file is too large")
)

// normalizeSubtitleUTF8 converts a downloaded text subtitle to UTF-8 without
// touching disk. UTF-8 is the fast path; GB18030 also covers GBK and GB2312.
func normalizeSubtitleUTF8(data []byte, contentType string, maxBytes int64) ([]byte, error) {
	if int64(len(data)) > maxBytes {
		return nil, errSubtitleTooLarge
	}

	var (
		normalized []byte
		err        error
	)
	switch {
	case bytes.HasPrefix(data, []byte{0x00, 0x00, 0xfe, 0xff}),
		bytes.HasPrefix(data, []byte{0xff, 0xfe, 0x00, 0x00}):
		return nil, fmt.Errorf("%w: UTF-32 is not supported", errSubtitleEncoding)
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		normalized = data[3:]
		if !utf8.Valid(normalized) {
			return nil, fmt.Errorf("%w: invalid UTF-8 BOM payload", errSubtitleEncoding)
		}
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		normalized, err = decodeUTF16(data[2:], binary.LittleEndian)
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		normalized, err = decodeUTF16(data[2:], binary.BigEndian)
	default:
		switch declaredSubtitleCharset(contentType) {
		case "utf-16le", "utf16le":
			normalized, err = decodeUTF16(data, binary.LittleEndian)
		case "utf-16be", "utf16be":
			normalized, err = decodeUTF16(data, binary.BigEndian)
		case "utf-16", "utf16":
			return nil, fmt.Errorf("%w: UTF-16 byte order is missing", errSubtitleEncoding)
		default:
			if utf8.Valid(data) {
				normalized = data
			} else {
				normalized, err = decodeSimplifiedChinese(data)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(normalized) || bytes.IndexByte(normalized, 0) >= 0 {
		return nil, errSubtitleEncoding
	}
	if int64(len(normalized)) > maxBytes {
		return nil, errSubtitleTooLarge
	}
	return normalized, nil
}

func declaredSubtitleCharset(contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(params["charset"]))
}

func decodeSimplifiedChinese(data []byte) ([]byte, error) {
	for _, candidate := range []encoding.Encoding{
		simplifiedchinese.GB18030,
		simplifiedchinese.GBK,
	} {
		decoded, err := candidate.NewDecoder().Bytes(data)
		if err != nil || !utf8.Valid(decoded) {
			continue
		}
		encoded, err := candidate.NewEncoder().Bytes(decoded)
		if err == nil && bytes.Equal(encoded, data) {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("%w: expected UTF-8, GB18030, or GBK", errSubtitleEncoding)
}

func decodeUTF16(data []byte, order binary.ByteOrder) ([]byte, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("%w: incomplete UTF-16 code unit", errSubtitleEncoding)
	}

	out := make([]byte, 0, len(data))
	for offset := 0; offset < len(data); offset += 2 {
		unit := order.Uint16(data[offset : offset+2])
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if offset+4 > len(data) {
				return nil, fmt.Errorf("%w: incomplete UTF-16 surrogate pair", errSubtitleEncoding)
			}
			next := order.Uint16(data[offset+2 : offset+4])
			if next < 0xdc00 || next > 0xdfff {
				return nil, fmt.Errorf("%w: invalid UTF-16 surrogate pair", errSubtitleEncoding)
			}
			out = utf8.AppendRune(out, utf16.DecodeRune(rune(unit), rune(next)))
			offset += 2
		case unit >= 0xdc00 && unit <= 0xdfff:
			return nil, fmt.Errorf("%w: unexpected UTF-16 low surrogate", errSubtitleEncoding)
		default:
			out = utf8.AppendRune(out, rune(unit))
		}
	}
	return out, nil
}
