package protocol

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

func appendJavaUTF(dst []byte, s string) ([]byte, error) {
	payload, err := encodeJavaUTFPayload(s)
	if err != nil {
		return nil, err
	}
	if len(payload) > 65535 {
		return nil, fmt.Errorf("java utf string too long: %d", len(payload))
	}

	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(payload)))
	dst = append(dst, header[:]...)
	dst = append(dst, payload...)
	return dst, nil
}

func encodeJavaUTFPayload(s string) ([]byte, error) {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*3)
	for _, u := range units {
		switch {
		case u == 0:
			out = append(out, 0xC0, 0x80)
		case u <= 0x7F:
			out = append(out, byte(u))
		case u <= 0x7FF:
			out = append(out,
				0xC0|byte(u>>6),
				0x80|byte(u&0x3F),
			)
		default:
			out = append(out,
				0xE0|byte(u>>12),
				0x80|byte((u>>6)&0x3F),
				0x80|byte(u&0x3F),
			)
		}
	}
	return out, nil
}

func decodeJavaUTF(data []byte) (string, error) {
	units := make([]uint16, 0, len(data))
	for i := 0; i < len(data); {
		b := data[i]
		switch {
		case b>>7 == 0:
			i++
			units = append(units, uint16(b))
		case b>>5 == 0x6:
			if i+1 >= len(data) {
				return "", fmt.Errorf("truncated modified utf-8 sequence")
			}
			b2 := data[i+1]
			if b == 0xC0 && b2 == 0x80 {
				units = append(units, 0)
			} else {
				units = append(units, uint16(b&0x1F)<<6|uint16(b2&0x3F))
			}
			i += 2
		case b>>4 == 0xE:
			if i+2 >= len(data) {
				return "", fmt.Errorf("truncated modified utf-8 sequence")
			}
			b2 := data[i+1]
			b3 := data[i+2]
			units = append(units, uint16(b&0x0F)<<12|uint16(b2&0x3F)<<6|uint16(b3&0x3F))
			i += 3
		default:
			return "", fmt.Errorf("unsupported modified utf-8 byte: 0x%x", b)
		}
	}
	return string(utf16.Decode(units)), nil
}
