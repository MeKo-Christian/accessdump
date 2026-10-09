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
