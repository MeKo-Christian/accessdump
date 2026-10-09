package mdb

import (
	"errors"
	"testing"
)

func ptr(s string) *string { return &s }

var testQueryEntries = []CatalogEntry{
	{ID: 10, Name: "Rebuilt", Type: ObjTypeQuery},
	{ID: 11, Name: "NoRows", Type: ObjTypeQuery, Flags: 0x08},
	{ID: 12, Name: "Broken", Type: ObjTypeQuery},
	{ID: 13, Name: "SomeTable", Type: ObjTypeLocalTable},
}

var testQueryRows = map[int32][]QueryRow{
	10: {
		{Attribute: qAttrType, Flag: 1},
		{Attribute: qAttrFlag, Flag: qSelectStar},
		{Attribute: qAttrTable, Name1: ptr("T")},
	},
	12: {
		{Attribute: qAttrType, Flag: 1},
		{Attribute: qAttrJoin, Name1: ptr("A"), Name2: ptr("B"), Flag: 9, Expression: ptr("A.x = B.x")},
	},
}

func TestBuildQueryDefsStatuses(t *testing.T) {
	t.Parallel()

	queries := buildQueryDefs(testQueryEntries, testQueryRows, nil)

	byName := make(map[string]QueryDef, len(queries))
	for _, q := range queries {
		byName[q.Name] = q
	}

	if len(queries) != 3 {
		t.Fatalf("expected the 3 queries only, got %d", len(queries))
	}

	if q := byName["Rebuilt"]; q.SQLStatus != SQLStatusFound || q.SQL != "SELECT *\nFROM T;" {
		t.Errorf("Rebuilt: got status %q SQL %q", q.SQLStatus, q.SQL)
	}

	if q := byName["NoRows"]; q.SQLStatus != SQLStatusNotInTable || q.SQL != "" || !q.Hidden {
		t.Errorf("NoRows: got status %q SQL %q hidden %v", q.SQLStatus, q.SQL, q.Hidden)
	}

	q := byName["Broken"]
	if q.SQLStatus != SQLStatusUnsupported || q.SQL != "" || q.Reason != "unknown join type 9" {
		t.Errorf("Broken: got status %q SQL %q reason %q", q.SQLStatus, q.SQL, q.Reason)
	}

	if queries[0].Name != "Broken" || queries[2].Name != "Rebuilt" {
		t.Errorf("expected queries sorted by name, got %q, %q, %q", queries[0].Name, queries[1].Name, queries[2].Name)
	}
}

func TestBuildQueryDefsTableMissing(t *testing.T) {
	t.Parallel()

	queries := buildQueryDefs(testQueryEntries, nil, errors.New("no MSysQueries"))

	for _, q := range queries {
		if q.SQLStatus != SQLStatusTableMissing {
			t.Errorf("query %q: expected SQLStatusTableMissing, got %q", q.Name, q.SQLStatus)
		}
	}
}

func TestQueryExpression(t *testing.T) {
	t.Parallel()

	jet3 := &Database{pageSize: PageSizeJet3}
	jet3CP1250 := jet3WithCodePage(1250)
	jet4 := &Database{pageSize: PageSizeJet4}

	tests := []struct {
		name        string
		db          *Database
		raw         []byte
		want        string
		wantPresent bool
		wantErr     bool
	}{
		// Short values are stored inline as they are: one byte per
		// character in Jet 3, UCS-2 in Jet 4.
		{name: "jet3 inline", db: jet3, raw: []byte("x=1"), want: "x=1", wantPresent: true},
		// Jet 3 text is in the database code page, not UTF-8.
		{name: "jet3 cp1252", db: jet3, raw: []byte("[Gr\xf6\xdfe]>1"), want: "[Größe]>1", wantPresent: true},
		{name: "jet3 cp1250", db: jet3CP1250, raw: []byte("[\x8akoda]"), want: "[Škoda]", wantPresent: true},
		{name: "jet4 inline", db: jet4, raw: []byte{'x', 0, '=', 0, '1', 0}, want: "x=1", wantPresent: true},
		{name: "NULL", db: jet4},
		// A reference to an LVAL page that does not exist is an error, not NULL.
		{name: "unresolvable", db: jet4, raw: []byte{3, 0, 0, LvalSingle, 0, 5, 0, 0, 0, 0, 0, 0}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			row := Row{}
			if test.raw != nil {
				row["Expression"] = test.raw
			}

			got, present, err := test.db.queryExpression(row)
			if (err != nil) != test.wantErr || present != test.wantPresent || got != test.want {
				t.Errorf("got %q, %v, %v; want %q, %v, error=%v", got, present, err, test.want, test.wantPresent, test.wantErr)
			}
		})
	}
}
