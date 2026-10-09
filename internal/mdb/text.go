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

// decodeJet3Text decodes a Jet 3 TEXT or MEMO value or a Jet 3 object name.
//
// Jet 3 stores text one byte per character (two for the East Asian code
// pages) in the code page recorded in the database header, not in UTF-8:
// Windows-1252 stores "Größe" as 47 72 F6 DF 65. Trailing NUL padding is
// dropped. For a code page without a decoder only ASCII survives; see
// CodePageErr.
func (db *Database) decodeJet3Text(b []byte) string {
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}

	b = b[:end]

	if db != nil && db.unsupportedCodePage {
		return decodeASCII(b)
	}

	charset := defaultJet3Charset
	if db != nil && db.charset != nil {
		charset = db.charset
	}

	decoded, err := charset.NewDecoder().Bytes(b)
	if err != nil {
		// The multi-byte decoders reject malformed sequences; the
		// single-byte fallback maps every byte.
		decoded, _ = defaultJet3Charset.NewDecoder().Bytes(b)
	}

	return string(decoded)
}
