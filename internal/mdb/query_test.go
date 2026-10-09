package mdb

import (
	"errors"
	"slices"
	"testing"
)

// Object flags as MSysObjects stores them for each query type.
const (
	flagsSelect    = 0
	flagsMakeTable = 80
	flagsAppend    = 64
	flagsUpdate    = 48
	flagsDelete    = 32
	flagsCrosstab  = 16
	flagsDDL       = 96
	flagsPassThru  = 112
	flagsUnion     = 128
)

func typeRow(flag int16) QueryRow { return QueryRow{Attribute: qAttrType, Flag: flag} }

func table(name string) QueryRow { return QueryRow{Attribute: qAttrTable, Name1: ptr(name)} }

func column(expr string) QueryRow { return QueryRow{Attribute: qAttrColumn, Expression: ptr(expr)} }

func join(from, to string, kind int16, on string) QueryRow {
	return QueryRow{Attribute: qAttrJoin, Name1: ptr(from), Name2: ptr(to), Flag: kind, Expression: ptr(on)}
}

func where(expr string) QueryRow { return QueryRow{Attribute: qAttrWhere, Expression: ptr(expr)} }

func TestReconstructQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		flags      int32
		rows       []QueryRow
		wantType   QueryType
		wantSQL    string
		wantParams []string
		wantConn   string
	}{
		{
			name:  "select with LEFT and RIGHT joins over three tables",
			flags: flagsSelect,
			rows: []QueryRow{
				{Attribute: qAttrStart},
				typeRow(1),
				table("Aufträge"),
				table("Kunden"),
				table("Positionen"),
				column("Aufträge.Nr"),
				{Attribute: qAttrColumn, Expression: ptr("Kunden.[Straße Nr]"), Name1: ptr("Straße")},
				join("Kunden", "Aufträge", 2, "Kunden.ID = Aufträge.KundeID"),
				join("Aufträge", "Positionen", 3, "Aufträge.Nr = Positionen.AuftragNr"),
				{Attribute: qAttrEnd},
			},
			wantType: QueryTypeSelect,
			wantSQL: "SELECT Aufträge.Nr, Kunden.[Straße Nr] AS Straße\n" +
				"FROM (Kunden LEFT JOIN Aufträge ON Kunden.ID = Aufträge.KundeID)" +
				" RIGHT JOIN Positionen ON Aufträge.Nr = Positionen.AuftragNr;",
		},
		{
			name:  "join on two columns",
			flags: flagsSelect,
			rows: []QueryRow{
				typeRow(1),
				table("A"),
				table("B"),
				{Attribute: qAttrFlag, Flag: qSelectStar},
				join("A", "B", 1, "A.x = B.x"),
				join("A", "B", 1, "A.y = B.y"),
			},
			wantType: QueryTypeSelect,
			wantSQL:  "SELECT *\nFROM A INNER JOIN B ON (A.y = B.y) AND (A.x = B.x);",
		},
		{
			name:  "parameters, subquery, DLookup, grouping and ordering",
			flags: flagsSelect,
			rows: []QueryRow{
				typeRow(1),
				{Attribute: qAttrParameter, Name1: ptr("[Ab Datum]"), Flag: ColTypeDatetime},
				{Attribute: qAttrParameter, Name1: ptr("Kunde"), Flag: ColTypeText, Extra: 255},
				{Attribute: qAttrFlag, Flag: qDistinct},
				{Attribute: qAttrTable, Name1: ptr("Rechnungen"), Name2: ptr("R")},
				column("R.KundeID"),
				{Attribute: qAttrColumn, Expression: ptr(`DLookUp("Beistellung","Material","ID=" & [R].[MatID])`), Name1: ptr("Beistellung")},
				{Attribute: qAttrColumn, Expression: ptr("Sum(R.Betrag)"), Name1: ptr("Summe")},
				where("R.Datum >= [Ab Datum] AND R.KundeID IN (SELECT ID FROM Kunden WHERE Name = [Kunde])"),
				{Attribute: qAttrGroupBy, Expression: ptr("R.KundeID")},
				{Attribute: qAttrHaving, Expression: ptr("Sum(R.Betrag) > 0")},
				{Attribute: qAttrOrderBy, Expression: ptr("Sum(R.Betrag)"), Name1: ptr("D")},
				{Attribute: qAttrOrderBy, Expression: ptr("R.KundeID")},
			},
			wantType:   QueryTypeSelect,
			wantParams: []string{"[Ab Datum] DateTime", "Kunde Text (255)"},
			wantSQL: "PARAMETERS [Ab Datum] DateTime, Kunde Text (255);\n" +
				`SELECT DISTINCT R.KundeID, DLookUp("Beistellung","Material","ID=" & [R].[MatID]) AS Beistellung, Sum(R.Betrag) AS Summe` + "\n" +
				"FROM Rechnungen AS R\n" +
				"WHERE R.Datum >= [Ab Datum] AND R.KundeID IN (SELECT ID FROM Kunden WHERE Name = [Kunde])\n" +
				"GROUP BY R.KundeID\n" +
				"HAVING Sum(R.Betrag) > 0\n" +
				"ORDER BY Sum(R.Betrag) DESC, R.KundeID;",
		},
		{
			name:  "top percent from another database",
			flags: flagsSelect,
			rows: []QueryRow{
				typeRow(1),
				{Attribute: qAttrFlag, Flag: qTop | qPercent | qSelectStar | qOwnerAccess, Name1: ptr("10")},
				{Attribute: qAttrRemoteDB, Name1: ptr(`\\server\x.mdb`)},
				table("T"),
			},
			wantType: QueryTypeSelect,
			wantSQL:  "SELECT TOP 10 PERCENT *\nFROM T IN '\\\\server\\x.mdb'\nWITH OWNERACCESS OPTION;",
		},
		{
			name:  "make-table",
			flags: flagsMakeTable,
			rows: []QueryRow{
				{Attribute: qAttrType, Flag: 2, Name1: ptr("tmp Export")},
				{Attribute: qAttrFlag, Flag: qSelectStar},
				table("Kunden"),
				where("Aktiv = True"),
			},
			wantType: QueryTypeMakeTable,
			wantSQL:  "SELECT * INTO [tmp Export]\nFROM Kunden\nWHERE Aktiv = True;",
		},
		{
			name:  "append from select",
			flags: flagsAppend,
			rows: []QueryRow{
				{Attribute: qAttrType, Flag: 3, Name1: ptr("Archiv")},
				table("Aufträge"),
				{Attribute: qAttrColumn, Expression: ptr("Aufträge.Nr"), Name2: ptr("Nr")},
				{Attribute: qAttrColumn, Expression: ptr("Date()"), Name2: ptr("Archiviert am")},
				where("Aufträge.Erledigt"),
			},
			wantType: QueryTypeAppend,
			wantSQL: "INSERT INTO Archiv (Nr, [Archiviert am])\n" +
				"SELECT Aufträge.Nr, Date()\nFROM Aufträge\nWHERE Aufträge.Erledigt;",
		},
		{
			name:  "append values",
			flags: flagsAppend,
			rows: []QueryRow{
				{Attribute: qAttrType, Flag: 3, Name1: ptr("Log")},
				{Attribute: qAttrColumn, Expression: ptr("'Start'"), Name2: ptr("Text"), Flag: qAppendValue},
				{Attribute: qAttrColumn, Expression: ptr("Now()"), Name2: ptr("Zeit"), Flag: qAppendValue},
			},
			wantType: QueryTypeAppend,
			wantSQL:  "INSERT INTO Log (Text, Zeit)\nVALUES ('Start', Now());",
		},
		{
			name:  "update over a join",
			flags: flagsUpdate,
			rows: []QueryRow{
				typeRow(4),
				table("Artikel"),
				table("Preise"),
				join("Artikel", "Preise", 1, "Artikel.Nr = Preise.Nr"),
				{Attribute: qAttrColumn, Expression: ptr("[Preise].[Neu]"), Name2: ptr("Artikel.Preis")},
				where("Preise.Gültig"),
			},
			wantType: QueryTypeUpdate,
			wantSQL: "UPDATE Artikel INNER JOIN Preise ON Artikel.Nr = Preise.Nr\n" +
				"SET Artikel.Preis = [Preise].[Neu]\nWHERE Preise.Gültig;",
		},
		{
			name:  "delete",
			flags: flagsDelete,
			rows: []QueryRow{
				typeRow(5),
				column("Temp.*"),
				table("Temp"),
				where("Temp.Datum < Date() - 30"),
			},
			wantType: QueryTypeDelete,
			wantSQL:  "DELETE Temp.*\nFROM Temp\nWHERE Temp.Datum < Date() - 30;",
		},
		{
			name:  "crosstab",
			flags: flagsCrosstab,
			rows: []QueryRow{
				typeRow(6),
				table("Umsatz"),
				{Attribute: qAttrColumn, Expression: ptr("Sum(Umsatz.Betrag)"), Name1: ptr("Summe")},
				{Attribute: qAttrColumn, Expression: ptr("Umsatz.Kunde"), Flag: qCrosstabNormal},
				{Attribute: qAttrColumn, Expression: ptr("Umsatz.Monat"), Flag: qCrosstabPivot},
				{Attribute: qAttrGroupBy, Expression: ptr("Umsatz.Kunde"), Flag: qCrosstabNormal},
				{Attribute: qAttrGroupBy, Expression: ptr("Umsatz.Monat"), Flag: qCrosstabPivot},
			},
			wantType: QueryTypeCrosstab,
			wantSQL: "TRANSFORM Sum(Umsatz.Betrag) AS Summe\n" +
				"SELECT Umsatz.Kunde\nFROM Umsatz\nGROUP BY Umsatz.Kunde\nPIVOT Umsatz.Monat;",
		},
		{
			name:  "union all with order by",
			flags: flagsUnion,
			rows: []QueryRow{
				typeRow(9),
				{Attribute: qAttrTable, Name2: ptr(unionPart1), Expression: ptr("SELECT Nr FROM A\r\n")},
				{Attribute: qAttrTable, Name2: ptr(unionPart2), Expression: ptr(" SELECT Nr FROM B")},
				{Attribute: qAttrOrderBy, Expression: ptr("Nr")},
			},
			wantType: QueryTypeUnion,
			wantSQL:  "SELECT Nr FROM A\nUNION ALL SELECT Nr FROM B\nORDER BY Nr;",
		},
		{
			name:  "union distinct",
			flags: flagsUnion,
			rows: []QueryRow{
				typeRow(9),
				{Attribute: qAttrFlag, Flag: qUnionDistinct},
				{Attribute: qAttrTable, Name2: ptr(unionPart1), Expression: ptr("SELECT 1")},
				{Attribute: qAttrTable, Name2: ptr(unionPart2), Expression: ptr("SELECT 2")},
			},
			wantType: QueryTypeUnion,
			wantSQL:  "SELECT 1\nUNION SELECT 2;",
		},
		{
			name:  "pass-through",
			flags: flagsPassThru,
			rows: []QueryRow{
				{
					Attribute: qAttrType, Flag: 8,
					Name1:      ptr("ODBC;DRIVER=SQL Server;SERVER=sql;UID=app;PWD=geheim"),
					Expression: ptr("EXEC dbo.Abrechnung 2026"),
				},
			},
			wantType: QueryTypePassThrough,
			wantSQL:  "EXEC dbo.Abrechnung 2026",
			wantConn: "ODBC;DRIVER=SQL Server;SERVER=sql;UID=app;PWD=geheim",
		},
		{
			name:  "bulk pass-through returning no records",
			flags: 0x90,
			rows: []QueryRow{
				{Attribute: qAttrType, Flag: 10, Name1: ptr("ODBC;DSN=x;Trusted_Connection=Yes"), Expression: ptr("EXEC dbo.Protokoll 1")},
			},
			wantType: QueryTypePassThrough,
			wantSQL:  "EXEC dbo.Protokoll 1",
			wantConn: "ODBC;DSN=x;Trusted_Connection=Yes",
		},
		{
			name:  "memo parameter",
			flags: flagsAppend,
			rows: []QueryRow{
				{Attribute: qAttrType, Flag: 3, Name1: ptr("NCR")},
				{Attribute: qAttrParameter, Name1: ptr("Text_P"), Flag: ColTypeMemo},
				{Attribute: qAttrColumn, Expression: ptr("[Text_P]"), Name2: ptr("Text"), Flag: qAppendValue},
			},
			wantType:   QueryTypeAppend,
			wantParams: []string{"Text_P LongText"},
			wantSQL:    "PARAMETERS Text_P LongText;\nINSERT INTO NCR (Text)\nVALUES ([Text_P]);",
		},
		{
			name:     "data definition",
			flags:    flagsDDL,
			rows:     []QueryRow{{Attribute: qAttrType, Flag: 7, Expression: ptr("CREATE INDEX ix ON T (A)\r\n")}},
			wantType: QueryTypeDataDefinition,
			wantSQL:  "CREATE INDEX ix ON T (A)\n",
		},
		{
			// MSysObjects.Flags says select, the type row knows better.
			name:     "type taken from the type row when the object flags are zero",
			flags:    0,
			rows:     []QueryRow{typeRow(5), column("T.*"), table("T")},
			wantType: QueryTypeDelete,
			wantSQL:  "DELETE T.*\nFROM T;",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ReconstructQuery(test.flags, test.rows)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.Type != test.wantType {
				t.Errorf("type: got %s, want %s", got.Type, test.wantType)
			}

			if got.SQL != test.wantSQL {
				t.Errorf("SQL:\ngot  %q\nwant %q", got.SQL, test.wantSQL)
			}

			if !slices.Equal(got.Parameters, test.wantParams) {
				t.Errorf("parameters: got %q, want %q", got.Parameters, test.wantParams)
			}

			if got.Connect != test.wantConn {
				t.Errorf("connect: got %q, want %q", got.Connect, test.wantConn)
			}
		})
	}
}

func TestReconstructQueryRefusesInsteadOfGuessing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags int32
		rows  []QueryRow
	}{
		{name: "no rows", flags: flagsSelect},
		{name: "unknown object flags", flags: 0xE0, rows: []QueryRow{typeRow(1)}},
		{name: "type row contradicts object flags", flags: flagsUpdate, rows: []QueryRow{typeRow(3)}},
		{name: "unknown join type", flags: flagsSelect, rows: []QueryRow{typeRow(1), table("A"), table("B"), join("A", "B", 4, "A.x = B.x")}},
		{
			name:  "unknown parameter type",
			flags: flagsSelect,
			rows:  []QueryRow{typeRow(1), {Attribute: qAttrParameter, Name1: ptr("p"), Flag: 99}, table("T")},
		},
		{
			name:  "union half missing",
			flags: flagsUnion,
			rows:  []QueryRow{typeRow(9), {Attribute: qAttrTable, Name2: ptr(unionPart1), Expression: ptr("SELECT 1")}},
		},
		{name: "two WHERE rows", flags: flagsSelect, rows: []QueryRow{typeRow(1), table("T"), where("a"), where("b")}},
		{name: "crosstab without PIVOT", flags: flagsCrosstab, rows: []QueryRow{typeRow(6), table("T"), column("Sum(x)")}},
		{name: "pass-through without SQL", flags: flagsPassThru, rows: []QueryRow{typeRow(8)}},
		{
			name:  "unreadable expression",
			flags: flagsDelete,
			rows:  []QueryRow{typeRow(5), table("T"), {Attribute: qAttrWhere, ExpressionErr: errors.New("broken LVAL chain")}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ReconstructQuery(test.flags, test.rows)
			if !errors.Is(err, errQueryUnsupported) {
				t.Fatalf("expected errQueryUnsupported, got %v", err)
			}

			if got.SQL != "" {
				t.Errorf("expected no partial SQL, got %q", got.SQL)
			}
		})
	}
}
