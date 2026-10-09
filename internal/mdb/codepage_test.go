package mdb

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

// jet3WithCodePage builds a Jet 3 Database whose text decoding is set up the
// way Open sets it up for the given header code page.
func jet3WithCodePage(codePage uint16) *Database {
	charset, ok := codePageEncoding(codePage)

	return &Database{
		Header:              Header{CodePage: codePage},
		pageSize:            PageSizeJet3,
		charset:             charset,
		unsupportedCodePage: !ok,
	}
}

func TestDecodeJet3Text(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		db   *Database
		raw  []byte
		want string
	}{
		{name: "ascii", db: jet3WithCodePage(1252), raw: []byte("Name"), want: "Name"},
		{name: "trailing NULs", db: jet3WithCodePage(1252), raw: []byte{'A', 'B', 0, 0}, want: "AB"},
		{name: "cp1252 umlauts", db: jet3WithCodePage(1252), raw: []byte("Gr\xf6\xdfe"), want: "Größe"},
		{name: "cp1250", db: jet3WithCodePage(1250), raw: []byte("\x8akoda"), want: "Škoda"},
		{name: "cp1251", db: jet3WithCodePage(1251), raw: []byte("\xc8\xec\xff"), want: "Имя"},
		{name: "cp932", db: jet3WithCodePage(932), raw: []byte("\x96\xbc\x91\x4f"), want: "\u540d\u524d"},
		// Johab has no decoder: only ASCII survives instead of a 1252 guess.
		{name: "unsupported code page", db: jet3WithCodePage(1361), raw: []byte("Gr\xf6\xdfe\x00"), want: "Gr��e"},
		{name: "no charset", db: &Database{}, raw: []byte("Gr\xf6\xdfe"), want: "Größe"},
		{name: "nil database", db: nil, raw: []byte("Gr\xf6\xdfe"), want: "Größe"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.db.decodeJet3Text(test.raw); got != test.want {
				t.Errorf("decodeJet3Text(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestCodePageErr(t *testing.T) {
	t.Parallel()

	unsupportedJet4 := jet3WithCodePage(1361)
	unsupportedJet4.pageSize = PageSizeJet4

	tests := []struct {
		name    string
		db      *Database
		wantErr bool
	}{
		{name: "supported", db: jet3WithCodePage(1252)},
		{name: "unsupported", db: jet3WithCodePage(1361), wantErr: true},
		// Jet 4 text is UCS-2, so its code page does not matter.
		{name: "unsupported jet4", db: unsupportedJet4},
		{name: "nil database", db: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.db.CodePageErr()
			if errors.Is(err, ErrUnsupportedCodePage) != test.wantErr {
				t.Errorf("CodePageErr() = %v, want error=%v", err, test.wantErr)
			}
		})
	}
}

func TestOpenJet3CodePage(t *testing.T) {
	for _, test := range []struct {
		codePage    uint16
		unsupported bool
	}{
		{codePage: 1252},
		{codePage: 1361, unsupported: true},
	} {
		path := writeSyntheticDB(t, PageSizeJet3, 4, JetVersion3)

		// Store the code page the way Access does: obfuscated.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		binary.LittleEndian.PutUint16(data[offsetCodePage:], test.codePage)

		err = decryptHeader(data, JetVersion3)
		if err != nil {
			t.Fatalf("decryptHeader: %v", err)
		}

		err = os.WriteFile(path, data, 0o600)
		if err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		db, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		if db.Header.CodePage != test.codePage {
			t.Errorf("CodePage = %d, want %d", db.Header.CodePage, test.codePage)
		}

		if gotErr := db.CodePageErr() != nil; gotErr != test.unsupported {
			t.Errorf("code page %d: CodePageErr() = %v, want error=%v", test.codePage, db.CodePageErr(), test.unsupported)
		}

		_ = db.Close()
	}
}
