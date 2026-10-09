package mdb

import "unicode/utf16"

// decodeJet4Text decodes a Jet4 TEXT or MEMO value.
//
// Jet4 stores text as UCS-2LE, except in columns with "Unicode compression"
// enabled: there a value may start with the marker FF FE, after which the
// bytes alternate between compressed runs (one byte per character, the low
// byte of a code point below U+0100) and plain UCS-2 runs, each 0x00 byte
// switching between the two. The value starts out compressed. Umlauts lie
// below U+0100, so a German name such as "Größe" is typically stored compressed
// and comes out garbled when read as plain UCS-2.
func decodeJet4Text(b []byte) string {
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		return decodeUCS2(b)
	}

	u16 := make([]uint16, 0, len(b))
	compressed := true

	for i := 2; i < len(b); {
		switch {
		case b[i] == 0x00:
			compressed = !compressed
			i++
		case compressed:
			u16 = append(u16, uint16(b[i]))
			i++
		case i+1 < len(b):
			u16 = append(u16, uint16(b[i])|uint16(b[i+1])<<8)
			i += 2
		default:
			// A dangling odd byte in an uncompressed run carries no character.
			i++
		}
	}

	return string(utf16.Decode(u16))
}
