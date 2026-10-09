package mdb

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Schema describes the complete schema of a Jet4 database.
type Schema struct {
	Tables        []TableSchema
	Relationships []Relationship
	Queries       []QueryDef
	Forms         []FormMeta
}

// TableSchema describes one user table.
type TableSchema struct {
	Name    string
	Columns []ColumnDef
}

// ColumnDef describes one column in a table.
type ColumnDef struct {
	Name          string
	JetType       byte
	SQLType       string // mapped SQL type string, e.g. "VARCHAR(50)", "INTEGER"
	Size          int    // character count for TEXT/BINARY; 0 for fixed-size types
	Required      bool
	AutoIncrement bool
}

// Relationship describes a foreign-key link between two tables,
// read from MSysRelationships.
type Relationship struct {
	Name          string
	FromTable     string
	FromColumns   []string
	ToTable       string
	ToColumns     []string
	CascadeUpdate bool
	CascadeDelete bool
}

// SQLStatus describes why a query's SQL text is present or absent.
type SQLStatus string

const (
	// SQLStatusFound means the SQL was reconstructed from MSysQueries.
	SQLStatusFound SQLStatus = "found"
	// SQLStatusTableMissing means MSysQueries could not be opened at all
	// (e.g. the table does not exist or the layout is unsupported).
	SQLStatusTableMissing SQLStatus = "table-missing"
	// SQLStatusNotInTable means MSysQueries was opened but holds no rows for
	// this query.
	SQLStatusNotInTable SQLStatus = "not-in-table"
	// SQLStatusUnsupported means the rows were found but could not be turned
	// into SQL; QueryDef.Reason says why. SQL is empty rather than partial.
	SQLStatusUnsupported SQLStatus = "unsupported"
)

// QueryDef is a named saved query.
type QueryDef struct {
	Name       string
	Type       QueryType
	SQL        string
	Parameters []string
	// Connect is the connect string of a pass-through query, unredacted.
	Connect   string
	Hidden    bool
	SQLStatus SQLStatus
	// Reason explains SQLStatusUnsupported.
	Reason string
}

// MSysRelationships cascade flags.
const (
	relFlagCascadeUpdate = 0x0100
	relFlagCascadeDelete = 0x1000
)

// colFlagAutoIncrement is the Jet4 column-flag bit for AutoNumber columns.
const colFlagAutoIncrement = 0x04

// ReadSchema reads all user tables, relationships, and saved queries from the
// database. Unreadable tables and system tables (MSys*) are silently skipped.
func (db *Database) ReadSchema() (*Schema, error) {
	entries, err := db.Catalog()
	if err != nil {
		return nil, fmt.Errorf("mdb: ReadSchema: catalog: %w", err)
	}

	s := &Schema{}

	for _, e := range entries {
		if e.Type != ObjTypeLocalTable || strings.HasPrefix(e.Name, "MSys") {
			continue
		}

		ts, tErr := db.readTableSchema(int64(e.ID), e.Name)
		if tErr != nil {
			continue
		}

		s.Tables = append(s.Tables, ts)
	}

	sort.Slice(s.Tables, func(i, j int) bool {
		return s.Tables[i].Name < s.Tables[j].Name
	})

	s.Relationships = db.readRelationships()
	s.Queries = db.readQueries(entries)

	// Form metadata is best-effort; ignore errors (MSysAccessStorage may be absent).
	s.Forms, _ = ScanFormBlobs(db)

	return s, nil
}

func (db *Database) readTableSchema(tdefPage int64, name string) (TableSchema, error) {
	td, err := db.ReadTableDef(tdefPage)
	if err != nil {
		return TableSchema{}, err
	}

	// Sort columns by ColNum for correct field order.
	cols := make([]*Column, len(td.Columns))
	copy(cols, td.Columns)
	sort.Slice(cols, func(i, j int) bool {
		return cols[i].ColNum < cols[j].ColNum
	})

	ts := TableSchema{Name: name}

	for _, col := range cols {
		size := 0
		if col.Type == ColTypeText || col.Type == ColTypeBinary {
			// Jet4 stores text length in bytes (UCS-2); divide by 2 for char count.
			size = int(col.Length) / 2
		}

		ts.Columns = append(ts.Columns, ColumnDef{
			Name:          col.Name,
			JetType:       col.Type,
			SQLType:       jetTypeToSQL(col.Type, int(col.Length), col.Scale, col.Precision),
			Size:          size,
			Required:      !col.IsNullable(),
			AutoIncrement: col.Flags&colFlagAutoIncrement != 0,
		})
	}

	return ts, nil
}

// readRelationships reads MSysRelationships. Each row represents one column
// pair; rows are grouped by relationship name.
func (db *Database) readRelationships() []Relationship {
	td, err := db.FindTable("MSysRelationships")
	if err != nil {
		return nil
	}

	rows, err := td.ReadRows()
	if err != nil {
		return nil
	}

	byName := make(map[string]*Relationship)
	var order []string

	for _, row := range rows {
		relName := stringField(row, "szRelationship")
		if relName == "" {
			continue
		}

		rel, exists := byName[relName]
		if !exists {
			grbit := intField(row, "grbit")
			rel = &Relationship{
				Name:          relName,
				FromTable:     stringField(row, "szObject"),
				ToTable:       stringField(row, "szReferencedObject"),
				CascadeUpdate: grbit&relFlagCascadeUpdate != 0,
				CascadeDelete: grbit&relFlagCascadeDelete != 0,
			}
			byName[relName] = rel
			order = append(order, relName)
		}

		if col := stringField(row, "szColumn"); col != "" {
			rel.FromColumns = append(rel.FromColumns, col)
		}

		if col := stringField(row, "szReferencedColumn"); col != "" {
			rel.ToColumns = append(rel.ToColumns, col)
		}
	}

	rels := make([]Relationship, 0, len(order))
	for _, name := range order {
		rels = append(rels, *byName[name])
	}

	return rels
}

// ReadQueries returns every saved query, including the embedded ~sq_ queries
// of forms, reports and controls, with its SQL rebuilt from MSysQueries.
func (db *Database) ReadQueries() ([]QueryDef, error) {
	entries, err := db.Catalog()
	if err != nil {
		return nil, fmt.Errorf("mdb: ReadQueries: catalog: %w", err)
	}

	return db.readQueries(entries), nil
}

// readQueries rebuilds the SQL of every saved query in the catalog from its
// MSysQueries rows; see query.go for the row layout.
//
// MSysQueries rows reference the catalog via ObjectId = MSysObjects.ID. Each
// returned QueryDef carries a SQLStatus explaining why SQL is present or absent.
func (db *Database) readQueries(entries []CatalogEntry) []QueryDef {
	rowsByObjID, tableErr := db.readQueryRows()

	return buildQueryDefs(entries, rowsByObjID, tableErr)
}

// buildQueryDefs turns the query catalog entries and their MSysQueries rows
// into sorted QueryDefs. A non-nil tableErr marks MSysQueries as unreadable.
func buildQueryDefs(entries []CatalogEntry, rowsByObjID map[int32][]QueryRow, tableErr error) []QueryDef {
	var queryEntries []CatalogEntry

	for _, e := range entries {
		if e.Type == ObjTypeQuery {
			queryEntries = append(queryEntries, e)
		}
	}

	queries := make([]QueryDef, 0, len(queryEntries))

	for _, e := range queryEntries {
		def := QueryDef{Name: e.Name, Hidden: e.Flags&objectFlagHidden != 0}

		rows := rowsByObjID[e.ID]

		switch {
		case tableErr != nil:
			def.SQLStatus = SQLStatusTableMissing
			def.Type = queryTypeOf(e.Flags, nil)
		case len(rows) == 0:
			def.SQLStatus = SQLStatusNotInTable
			def.Type = queryTypeOf(e.Flags, nil)
		default:
			rebuilt, err := ReconstructQuery(e.Flags, rows)
			def.Type = rebuilt.Type

			if err != nil {
				def.SQLStatus = SQLStatusUnsupported
				def.Reason = strings.TrimPrefix(err.Error(), errQueryUnsupported.Error()+": ")
			} else {
				def.SQLStatus = SQLStatusFound
				def.SQL = rebuilt.SQL
				def.Parameters = rebuilt.Parameters
				def.Connect = rebuilt.Connect
			}
		}

		queries = append(queries, def)
	}

	sort.Slice(queries, func(i, j int) bool {
		return queries[i].Name < queries[j].Name
	})

	return queries
}

// readQueryRows reads MSysQueries, grouped by ObjectId in table order.
func (db *Database) readQueryRows() (map[int32][]QueryRow, error) {
	td, err := db.FindTable("MSysQueries")
	if err != nil {
		return nil, err
	}

	rows, err := td.ReadRows()
	if err != nil {
		return nil, err
	}

	byObjID := make(map[int32][]QueryRow)

	for _, row := range rows {
		oid := intField(row, "ObjectId")

		byObjID[oid] = append(byObjID[oid], QueryRow{
			// Attribute is a Byte column and Flag an Int column, so the
			// reader hands them over as uint8 and int16.
			Attribute:  byteField(row, "Attribute"),
			Expression: db.memoTextField(row, "Expression"),
			Flag:       int16Field(row, "Flag"),
			Extra:      intField(row, "LvExtra"),
			Name1:      optionalStringField(row, "Name1"),
			Name2:      optionalStringField(row, "Name2"),
		})
	}

	return byObjID, nil
}

// memoTextField resolves a MEMO column to text, or nil when it is NULL or
// cannot be resolved.
func (db *Database) memoTextField(row Row, key string) *string {
	raw, _ := row[key].([]byte)
	if len(raw) == 0 {
		return nil
	}

	resolved, err := db.ResolveMemo(raw)
	if err != nil || len(resolved) == 0 {
		return nil
	}

	text := decodeJet4Text(resolved)

	return &text
}

func byteField(row Row, key string) uint8 {
	v, _ := row[key].(uint8)

	return v
}

func int16Field(row Row, key string) int16 {
	v, _ := row[key].(int16)

	return v
}

func optionalStringField(row Row, key string) *string {
	v, ok := row[key].(string)
	if !ok {
		return nil
	}

	return &v
}

// stringField extracts a string value from a Row, returning "" if absent or wrong type.
func stringField(row Row, key string) string {
	v, _ := row[key].(string)
	return v
}

// intField extracts an integer value from a Row, handling int16/int32.
func intField(row Row, key string) int32 {
	switch v := row[key].(type) {
	case int32:
		return v
	case int16:
		return int32(v)
	case int8:
		return int32(v)
	case uint8:
		return int32(v)
	case uint32:
		if v > math.MaxInt32 {
			return 0
		}

		return int32(v)
	}

	return 0
}

// jetTypeToSQL maps a Jet4 column type to a SQL type string.
func jetTypeToSQL(t byte, length int, scale, precision byte) string {
	switch t {
	case ColTypeBool:
		return "BOOLEAN"
	case ColTypeByte:
		return "TINYINT"
	case ColTypeInt:
		return "SMALLINT"
	case ColTypeLong:
		return "INTEGER"
	case ColTypeMoney:
		return "DECIMAL(19,4)"
	case ColTypeFloat:
		return "REAL"
	case ColTypeDouble:
		return "DOUBLE PRECISION"
	case ColTypeDatetime:
		return "DATETIME"
	case ColTypeText:
		chars := length / 2
		if chars <= 0 {
			chars = 255
		}

		return fmt.Sprintf("VARCHAR(%d)", chars)
	case ColTypeMemo:
		return "TEXT"
	case ColTypeBinary:
		chars := length / 2
		if chars <= 0 {
			chars = 255
		}

		return fmt.Sprintf("BINARY(%d)", chars)
	case ColTypeOLE:
		return "OLE"
	case ColTypeGUID:
		return "CHAR(38)"
	case ColTypeNumeric:
		if precision == 0 {
			return "DECIMAL"
		}

		return fmt.Sprintf("DECIMAL(%d,%d)", precision, scale)
	default:
		return fmt.Sprintf("TYPE_0x%02X", t)
	}
}
