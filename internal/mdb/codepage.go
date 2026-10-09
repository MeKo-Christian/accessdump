package mdb

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// ErrUnsupportedCodePage reports a Jet 3 database whose header names a code
// page without a decoder. Its non-ASCII text is decoded as U+FFFD.
var ErrUnsupportedCodePage = errors.New("mdb: unsupported code page")

// defaultJet3Charset decodes Jet 3 text for a Database built without a
// charset, which Open never does. Windows-1252 is what Access 97 uses for
// Western European locales.
var defaultJet3Charset encoding.Encoding = charmap.Windows1252

// CodePageErr returns ErrUnsupportedCodePage when this is a Jet 3 database
// and its text cannot be decoded faithfully. Jet 4 and later store text as
// UCS-2 and do not depend on the code page.
func (db *Database) CodePageErr() error {
	if db == nil || !db.unsupportedCodePage || !db.IsJet3() {
		return nil
	}

	return fmt.Errorf("%w %d: non-ASCII Jet 3 text is shown as U+FFFD", ErrUnsupportedCodePage, db.Header.CodePage)
}

// decodeASCII keeps ASCII bytes and replaces all others with U+FFFD. It is
// used for code pages without a decoder: guessing another code page would
// produce plausible but wrong text.
func decodeASCII(b []byte) string {
	out := make([]rune, len(b))
	for i, c := range b {
		out[i] = rune(c)
		if c >= utf8.RuneSelf {
			out[i] = utf8.RuneError
		}
	}

	return string(out)
}

// codePageEncoding maps the Windows code page from the database header to a
// text encoding. ok is false when no decoder exists for it.
func codePageEncoding(codePage uint16) (enc encoding.Encoding, ok bool) {
	enc = lookupCodePage(codePage)

	return enc, enc != nil
}

func lookupCodePage(codePage uint16) encoding.Encoding {
	switch codePage {
	case 437:
		return charmap.CodePage437
	case 850:
		return charmap.CodePage850
	case 852:
		return charmap.CodePage852
	case 866:
		return charmap.CodePage866
	case 874:
		return charmap.Windows874
	case 932:
		return japanese.ShiftJIS
	case 936:
		return simplifiedchinese.GBK
	case 949:
		return korean.EUCKR
	case 950:
		return traditionalchinese.Big5
	case 1250:
		return charmap.Windows1250
	case 1251:
		return charmap.Windows1251
	case 1252:
		return charmap.Windows1252
	case 1253:
		return charmap.Windows1253
	case 1254:
		return charmap.Windows1254
	case 1255:
		return charmap.Windows1255
	case 1256:
		return charmap.Windows1256
	case 1257:
		return charmap.Windows1257
	case 1258:
		return charmap.Windows1258
	default:
		return nil
	}
}
