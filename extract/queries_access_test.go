package extract

// Compares the reconstructed SQL with what Access itself shows, using an
// export written by ExportQuerySQL in testdata/queries/AccessdumpQueries.bas.
//
// By default it checks testdata/queries/fixture.mdb against
// fixture.mdb.expected.txt. Point ACCESSDUMP_COMPARE_MDB at any other database
// (e.g. a production frontend) to check that one against
// <database>.expected.txt instead. Without the files the test is skipped.

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

type accessQuery struct {
	Name    string
	Type    int
	Connect string
	SQL     string
}

func TestQueriesMatchAccess(t *testing.T) {
	t.Parallel()

	mdbPath := os.Getenv("ACCESSDUMP_COMPARE_MDB")
	if mdbPath == "" {
		mdbPath = filepath.Join("..", "testdata", "queries", "fixture.mdb")
	}

	expectedPath := mdbPath + ".expected.txt"

	expected, err := readAccessExport(expectedPath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("no Access export at %s; see testdata/queries/AccessdumpQueries.bas", expectedPath)
	}

	if err != nil {
		t.Fatal(err)
	}

	queries, err := Queries(mdbPath, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]Query, len(queries))
	for _, q := range queries {
		got[q.Name] = q
	}

	matched := 0

	for _, want := range expected {
		q, ok := got[want.Name]

		switch {
		case !ok:
			t.Errorf("%s: missing from the export", want.Name)
		case q.SQLStatus != SQLStatusFound:
			t.Errorf("%s: not reconstructed: %s %s", want.Name, q.SQLStatus, q.Reason)
		case string(q.Type) != accessTypeName(want.Type):
			t.Errorf("%s: type %s, Access says %d", want.Name, q.Type, want.Type)
		case normalizeSQL(q.SQL) != normalizeSQL(want.SQL):
			t.Errorf("%s: SQL differs\n got:    %s\n access: %s", want.Name, q.SQL, want.SQL)
		case q.Connect != want.Connect:
			t.Errorf("%s: connect %q, Access says %q", want.Name, q.Connect, want.Connect)
		default:
			matched++
		}

		delete(got, want.Name)
	}

	for name := range got {
		t.Errorf("%s: exported but unknown to Access", name)
	}

	t.Logf("%d of %d queries match Access", matched, len(expected))
}

// accessTypeName maps QueryDef.Type to our type names. The DAO constants are
// the same values MSysObjects.Flags carries.
func accessTypeName(t int) string {
	names := map[int]QueryType{
		0:   QueryTypeSelect,
		16:  QueryTypeCrosstab,
		32:  QueryTypeDelete,
		48:  QueryTypeUpdate,
		64:  QueryTypeAppend,
		80:  QueryTypeMakeTable,
		96:  QueryTypeDataDefinition,
		112: QueryTypePassThrough,
		128: QueryTypeUnion,
	}

	if name, ok := names[t]; ok {
		return string(name)
	}

	return "dao-type-" + strconv.Itoa(t)
}

func readAccessExport(path string) ([]accessQuery, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		queries []accessQuery
		current *accessQuery
		sqlBuf  []string
	)

	flush := func() {
		if current != nil {
			current.SQL = strings.Join(sqlBuf, "\n")
			queries = append(queries, *current)
		}

		sqlBuf = nil
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)

	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\uFEFF")

		switch {
		case strings.HasPrefix(line, "### QUERY "):
			flush()

			current = &accessQuery{Name: strings.TrimPrefix(line, "### QUERY ")}
		case current != nil && strings.HasPrefix(line, "### TYPE "):
			current.Type, err = strconv.Atoi(strings.TrimPrefix(line, "### TYPE "))
			if err != nil {
				return nil, err
			}
		case current != nil && strings.HasPrefix(line, "### CONNECT "):
			current.Connect = strings.TrimPrefix(line, "### CONNECT ")
		default:
			sqlBuf = append(sqlBuf, line)
		}
	}

	flush()

	return queries, scanner.Err()
}

// normalizeSQL reduces SQL to what must match: brackets, parentheses, case
// and whitespace may differ between Access and the reconstruction.
func normalizeSQL(sql string) string {
	sql = strings.ToLower(sql)
	sql = strings.NewReplacer("[", "", "]", "", "(", " ", ")", " ").Replace(sql)
	sql = strings.Join(strings.Fields(sql), " ")

	// Drop the spaces around punctuation so "a = b" and "a=b" compare equal.
	var b strings.Builder

	runes := []rune(sql)
	for i, r := range runes {
		if r == ' ' && i > 0 && i < len(runes)-1 && (isPunct(runes[i-1]) || isPunct(runes[i+1])) {
			continue
		}

		b.WriteRune(r)
	}

	return strings.TrimRight(b.String(), "; ")
}

func isPunct(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != ' '
}

func TestNormalizeSQL(t *testing.T) {
	t.Parallel()

	a := "PARAMETERS [Kunde] Text ( 255 );\r\nSELECT Kunden.[Straße Nr]\nFROM (A LEFT JOIN B ON A.x=B.x);"
	b := "parameters Kunde Text (255);\nSELECT Kunden.Straße Nr FROM A LEFT JOIN B ON A.x = B.x"

	if normalizeSQL(a) != normalizeSQL(b) {
		t.Errorf("expected equal:\n%q\n%q", normalizeSQL(a), normalizeSQL(b))
	}

	if normalizeSQL("SELECT a FROM T") == normalizeSQL("SELECT b FROM T") {
		t.Error("different columns must not compare equal")
	}
}
