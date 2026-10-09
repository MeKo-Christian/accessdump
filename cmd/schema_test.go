package cmd

import (
	"strings"
	"testing"

	"github.com/MeKo-Christian/accessdump/internal/mdb"
)

// quoteIdent

func TestQuoteIdent_plain(t *testing.T) {
	if got := quoteIdent("MyTable"); got != "[MyTable]" {
		t.Errorf("expected [MyTable], got %q", got)
	}
}

func TestQuoteIdent_containsBracket(t *testing.T) {
	// Closing bracket must be escaped by doubling
	got := quoteIdent("My]Table")
	if !strings.Contains(got, "]]") {
		t.Errorf("expected ]] escape in %q", got)
	}
}

func TestQuoteIdent_empty(t *testing.T) {
	if got := quoteIdent(""); got != "[]" {
		t.Errorf("expected [], got %q", got)
	}
}

// joinIdents

func TestJoinIdents_single(t *testing.T) {
	if got := joinIdents([]string{"Col1"}); got != "[Col1]" {
		t.Errorf("expected [Col1], got %q", got)
	}
}

func TestJoinIdents_multiple(t *testing.T) {
	got := joinIdents([]string{"Col1", "Col2"})
	if got != "[Col1], [Col2]" {
		t.Errorf("expected [Col1], [Col2], got %q", got)
	}
}

func TestJoinIdents_empty(t *testing.T) {
	if got := joinIdents([]string{}); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// renderDDL

func TestRenderDDL_emptySchema(t *testing.T) {
	s := &mdb.Schema{}
	out := renderDDL("testdb", s)

	if !strings.Contains(out, "testdb") {
		t.Errorf("expected db name in output, got: %q", out)
	}
}

func TestRenderDDL_tableWithColumns(t *testing.T) {
	s := &mdb.Schema{
		Tables: []mdb.TableSchema{
			{
				Name: "Customers",
				Columns: []mdb.ColumnDef{
					{Name: "ID", SQLType: "INTEGER", Required: true, AutoIncrement: true},
					{Name: "Name", SQLType: "VARCHAR(50)"},
				},
			},
		},
	}

	out := renderDDL("testdb", s)

	if !strings.Contains(out, "CREATE TABLE [Customers]") {
		t.Errorf("expected CREATE TABLE statement, got: %q", out)
	}

	if !strings.Contains(out, "NOT NULL") {
		t.Errorf("expected NOT NULL for required column, got: %q", out)
	}

	if !strings.Contains(out, "AUTOINCREMENT") {
		t.Errorf("expected AUTOINCREMENT for auto column, got: %q", out)
	}
}

func TestRenderDDL_relationship(t *testing.T) {
	s := &mdb.Schema{
		Relationships: []mdb.Relationship{
			{
				Name:          "FK_Orders_Customers",
				FromTable:     "Orders",
				FromColumns:   []string{"CustomerID"},
				ToTable:       "Customers",
				ToColumns:     []string{"ID"},
				CascadeUpdate: true,
				CascadeDelete: false,
			},
		},
	}

	out := renderDDL("testdb", s)

	if !strings.Contains(out, "ALTER TABLE [Orders]") {
		t.Errorf("expected ALTER TABLE, got: %q", out)
	}

	if !strings.Contains(out, "ON UPDATE CASCADE") {
		t.Errorf("expected ON UPDATE CASCADE, got: %q", out)
	}

	if strings.Contains(out, "ON DELETE CASCADE") {
		t.Errorf("unexpected ON DELETE CASCADE, got: %q", out)
	}
}

func TestRenderDDL_selectQueryBecomesView(t *testing.T) {
	s := &mdb.Schema{
		Queries: []mdb.QueryDef{
			{Name: "ActiveCustomers", Type: mdb.QueryTypeSelect, SQL: "SELECT * FROM Customers WHERE Active = 1;"},
		},
	}

	out := renderDDL("testdb", s)

	if !strings.Contains(out, "CREATE VIEW [ActiveCustomers]") {
		t.Errorf("expected SELECT query rendered as CREATE VIEW, got: %q", out)
	}

	if strings.Contains(out, ";;") {
		t.Errorf("the reconstructed SQL already ends in ';', got: %q", out)
	}
}

func TestRenderDDL_actionQueryIsCommented(t *testing.T) {
	s := &mdb.Schema{
		Queries: []mdb.QueryDef{
			{Name: "DeleteOld", Type: mdb.QueryTypeDelete, SQL: "DELETE FROM Log WHERE Date < #2020-01-01#;"},
		},
	}

	out := renderDDL("testdb", s)

	if strings.Contains(out, "CREATE VIEW") {
		t.Errorf("action query should not become a VIEW, got: %q", out)
	}

	if !strings.Contains(out, "-- delete query: DeleteOld") {
		t.Errorf("expected action query comment, got: %q", out)
	}
}

func TestRenderDDL_queryWithEmptySQL(t *testing.T) {
	s := &mdb.Schema{
		Queries: []mdb.QueryDef{
			{Name: "Orphan", SQL: ""},
		},
	}

	out := renderDDL("testdb", s)

	if !strings.Contains(out, "SQL not available") {
		t.Errorf("expected 'SQL not available' comment, got: %q", out)
	}
}

// writeFormMarkdown

func TestWriteFormMarkdown_empty(t *testing.T) {
	var b strings.Builder
	writeFormMarkdown(&b, nil)

	if b.Len() != 0 {
		t.Errorf("expected no output for nil forms, got: %q", b.String())
	}
}

func TestWriteFormMarkdown_formWithRecordSource(t *testing.T) {
	var b strings.Builder
	forms := []mdb.FormMeta{
		{Name: "Orders", RecordSource: "SELECT OrderID, Total FROM Orders;"},
	}

	writeFormMarkdown(&b, forms)
	out := b.String()

	if !strings.Contains(out, "## Forms") {
		t.Errorf("expected ## Forms heading, got: %q", out)
	}

	if !strings.Contains(out, "### Orders") {
		t.Errorf("expected form name heading, got: %q", out)
	}

	if !strings.Contains(out, "SELECT OrderID, Total FROM Orders;") {
		t.Errorf("expected RecordSource SQL in output, got: %q", out)
	}
}

func TestWriteFormMarkdown_formWithEventHandlers(t *testing.T) {
	var b strings.Builder
	forms := []mdb.FormMeta{
		{Name: "Startschirm", EventHandlers: []string{"[Event Procedure]", "=HandleButtonClick(1)"}},
	}

	writeFormMarkdown(&b, forms)
	out := b.String()

	if !strings.Contains(out, "[Event Procedure]") {
		t.Errorf("expected event handler in output, got: %q", out)
	}

	if !strings.Contains(out, "=HandleButtonClick(1)") {
		t.Errorf("expected expression event in output, got: %q", out)
	}
}

func TestWriteFormMarkdown_formWithNoMetadata(t *testing.T) {
	var b strings.Builder
	forms := []mdb.FormMeta{
		{Name: "Bare"},
	}

	writeFormMarkdown(&b, forms)
	out := b.String()

	// Form with no RecordSource or EventHandlers should still appear.
	if !strings.Contains(out, "### Bare") {
		t.Errorf("expected bare form name heading, got: %q", out)
	}
}

// renderMarkdown

func TestRenderMarkdown_heading(t *testing.T) {
	s := &mdb.Schema{}
	out := renderMarkdown("mydb", s)

	if !strings.HasPrefix(out, "# mydb") {
		t.Errorf("expected markdown heading with db name, got: %q", out)
	}
}

func TestRenderMarkdown_emptySchema(t *testing.T) {
	s := &mdb.Schema{}
	out := renderMarkdown("mydb", s)

	// Empty sections should be omitted
	if strings.Contains(out, "## Tables") {
		t.Errorf("empty Tables section should not appear, got: %q", out)
	}
}

// writeTableMarkdown

func TestWriteTableMarkdown_empty(t *testing.T) {
	var b strings.Builder
	writeTableMarkdown(&b, nil)

	if b.Len() != 0 {
		t.Errorf("expected no output for nil tables, got: %q", b.String())
	}
}

func TestWriteTableMarkdown_hasHeaders(t *testing.T) {
	var b strings.Builder
	tables := []mdb.TableSchema{
		{
			Name: "Orders",
			Columns: []mdb.ColumnDef{
				{Name: "ID", SQLType: "INTEGER", Size: 0, Required: true, AutoIncrement: true},
				{Name: "Amount", SQLType: "DOUBLE", Size: 8},
			},
		},
	}

	writeTableMarkdown(&b, tables)
	out := b.String()

	if !strings.Contains(out, "### Orders") {
		t.Errorf("expected table name heading, got: %q", out)
	}

	if !strings.Contains(out, "| Column |") {
		t.Errorf("expected column header row, got: %q", out)
	}

	if !strings.Contains(out, "*(auto)*") {
		t.Errorf("expected auto-increment marker, got: %q", out)
	}

	if !strings.Contains(out, "✓") {
		t.Errorf("expected required marker, got: %q", out)
	}
}

func TestWriteTableMarkdown_dashForZeroSize(t *testing.T) {
	var b strings.Builder
	tables := []mdb.TableSchema{
		{
			Name:    "T",
			Columns: []mdb.ColumnDef{{Name: "ID", SQLType: "INTEGER", Size: 0}},
		},
	}

	writeTableMarkdown(&b, tables)

	if !strings.Contains(b.String(), "—") {
		t.Errorf("expected em dash for zero size, got: %q", b.String())
	}
}

// writeRelationshipMarkdown

func TestWriteRelationshipMarkdown_empty(t *testing.T) {
	var b strings.Builder
	writeRelationshipMarkdown(&b, nil)

	if b.Len() != 0 {
		t.Errorf("expected no output for nil relationships, got: %q", b.String())
	}
}

func TestWriteRelationshipMarkdown_basic(t *testing.T) {
	var b strings.Builder
	rels := []mdb.Relationship{
		{
			FromTable:   "Orders",
			FromColumns: []string{"CustomerID"},
			ToTable:     "Customers",
			ToColumns:   []string{"ID"},
		},
	}

	writeRelationshipMarkdown(&b, rels)
	out := b.String()

	if !strings.Contains(out, "## Relationships") {
		t.Errorf("expected Relationships heading, got: %q", out)
	}

	if !strings.Contains(out, "Orders") || !strings.Contains(out, "Customers") {
		t.Errorf("expected table names in output, got: %q", out)
	}
}

func TestWriteRelationshipMarkdown_cascadeAnnotations(t *testing.T) {
	var b strings.Builder
	rels := []mdb.Relationship{
		{
			FromTable: "A", FromColumns: []string{"x"},
			ToTable: "B", ToColumns: []string{"y"},
			CascadeUpdate: true,
			CascadeDelete: true,
		},
	}

	writeRelationshipMarkdown(&b, rels)
	out := b.String()

	if !strings.Contains(out, "cascade update") {
		t.Errorf("expected cascade update annotation, got: %q", out)
	}

	if !strings.Contains(out, "cascade delete") {
		t.Errorf("expected cascade delete annotation, got: %q", out)
	}
}

// writeQueryMarkdown

func TestWriteQueryMarkdown_empty(t *testing.T) {
	var b strings.Builder
	writeQueryMarkdown(&b, nil)

	if b.Len() != 0 {
		t.Errorf("expected no output for nil queries, got: %q", b.String())
	}
}

func TestWriteQueryMarkdown_withSQL(t *testing.T) {
	var b strings.Builder
	queries := []mdb.QueryDef{
		{Name: "GetAll", SQL: "SELECT * FROM T"},
	}

	writeQueryMarkdown(&b, queries)
	out := b.String()

	if !strings.Contains(out, "### GetAll") {
		t.Errorf("expected query name heading, got: %q", out)
	}

	if !strings.Contains(out, "```sql") {
		t.Errorf("expected sql code fence, got: %q", out)
	}
}

func TestWriteQueryMarkdown_noSQL(t *testing.T) {
	var b strings.Builder
	queries := []mdb.QueryDef{
		{Name: "Mystery", SQL: "", SQLStatus: mdb.SQLStatusNotInTable},
	}

	writeQueryMarkdown(&b, queries)
	out := b.String()

	if !strings.Contains(out, "SQL not available") {
		t.Errorf("expected 'SQL not available', got: %q", out)
	}
}

func TestWriteQueryMarkdown_statusTableMissing(t *testing.T) {
	var b strings.Builder
	queries := []mdb.QueryDef{
		{Name: "Q", SQL: "", SQLStatus: mdb.SQLStatusTableMissing},
	}

	writeQueryMarkdown(&b, queries)
	out := b.String()

	if !strings.Contains(out, "table-missing") {
		t.Errorf("expected status reason 'table-missing' in output, got: %q", out)
	}
}

func TestWriteQueryMarkdown_statusNotInTable(t *testing.T) {
	var b strings.Builder
	queries := []mdb.QueryDef{
		{Name: "Q", SQL: "", SQLStatus: mdb.SQLStatusNotInTable},
	}

	writeQueryMarkdown(&b, queries)
	out := b.String()

	if !strings.Contains(out, "not-in-table") {
		t.Errorf("expected status reason 'not-in-table' in output, got: %q", out)
	}
}

func TestRenderDDL_queryStatusInComment(t *testing.T) {
	s := &mdb.Schema{
		Queries: []mdb.QueryDef{
			{Name: "Orphan", SQL: "", SQLStatus: mdb.SQLStatusTableMissing},
		},
	}

	out := renderDDL("testdb", s)

	if !strings.Contains(out, "table-missing") {
		t.Errorf("expected status reason in DDL comment, got: %q", out)
	}
}
