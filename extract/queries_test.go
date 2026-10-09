package extract

import "testing"

func TestParseEmbeddedQueryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		want      QueryOwner
		wantFound bool
	}{
		{name: "~sq_fAuftrag", want: QueryOwner{Kind: OwnerForm, Object: "Auftrag"}, wantFound: true},
		{name: "~sq_rRechnung Kopie", want: QueryOwner{Kind: OwnerReport, Object: "Rechnung Kopie"}, wantFound: true},
		{
			name:      "~sq_cAuftrag~sq_cKombiKunde",
			want:      QueryOwner{Kind: OwnerForm, Object: "Auftrag", Control: "KombiKunde"},
			wantFound: true,
		},
		{
			name:      "~sq_dLieferschein~sq_dListePositionen",
			want:      QueryOwner{Kind: OwnerReport, Object: "Lieferschein", Control: "ListePositionen"},
			wantFound: true,
		},
		{name: "~sq_xIrgendwas", want: QueryOwner{Kind: OwnerUnknown, Object: "Irgendwas"}, wantFound: true},
		{name: "QRY_Lieferscheine_erstellen_Gesamt", wantFound: false},
		{name: "~TMPCLP123", wantFound: false},
		{name: "~sq_", wantFound: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, found := ParseEmbeddedQueryName(test.name)
			if found != test.wantFound || got != test.want {
				t.Errorf("got (%+v, %v), want (%+v, %v)", got, found, test.want, test.wantFound)
			}
		})
	}
}

func TestRedactPasswords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{
			in:   "ODBC;DRIVER=SQL Server;SERVER=sql;UID=app;PWD=geheim;DATABASE=IPOffice",
			want: "ODBC;DRIVER=SQL Server;SERVER=sql;UID=app;PWD=***;DATABASE=IPOffice",
		},
		{in: "ODBC;Password = s3cr3t", want: "ODBC;Password = ***"},
		{in: "ODBC;pwd={a;b}c;UID=x", want: "ODBC;pwd=***c;UID=x"},
		{
			in:   "SELECT * INTO T IN '' [ODBC;DSN=x;PWD=geheim]\nFROM A;",
			want: "SELECT * INTO T IN '' [ODBC;DSN=x;PWD=***]\nFROM A;",
		},
		{in: "SELECT Passwort FROM Benutzer;", want: "SELECT Passwort FROM Benutzer;"},
		{in: "", want: ""},
	}

	for _, test := range tests {
		if got := RedactPasswords(test.in); got != test.want {
			t.Errorf("RedactPasswords(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
