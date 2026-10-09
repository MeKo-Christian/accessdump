package mdb

import "testing"

func TestDecodeJet3Text(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		db   *Database
		raw  []byte
		want string
	}{
		{name: "ascii", db: &Database{charset: codePageEncoding(1252)}, raw: []byte("Name"), want: "Name"},
		{name: "trailing NULs", db: &Database{charset: codePageEncoding(1252)}, raw: []byte{'A', 'B', 0, 0}, want: "AB"},
		{name: "cp1252 umlauts", db: &Database{charset: codePageEncoding(1252)}, raw: []byte("Gr\xf6\xdfe"), want: "Größe"},
		{name: "cp1250", db: &Database{charset: codePageEncoding(1250)}, raw: []byte("\x8akoda"), want: "Škoda"},
		{name: "cp1251", db: &Database{charset: codePageEncoding(1251)}, raw: []byte("\xc8\xec\xff"), want: "Имя"},
		{name: "cp932", db: &Database{charset: codePageEncoding(932)}, raw: []byte("\x96\xbc\x91\x4f"), want: "\u540d\u524d"},
		{name: "unknown code page", db: &Database{charset: codePageEncoding(18079)}, raw: []byte("Gr\xf6\xdfe"), want: "Größe"},
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
