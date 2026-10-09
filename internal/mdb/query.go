package mdb

// SQL reconstruction for saved queries.
//
// Access does not keep the SQL text of a saved query. It stores the query
// broken into rows of MSysQueries, all sharing the query's ObjectId, and
// rebuilds the SQL whenever it is shown. This file does the same. The row
// layout and the rebuilding rules follow Jackcess
// (com.healthmarketscience.jackcess.impl.query, Apache License 2.0), which
// mdbtools (src/util/mdb-queries.c) confirms for the attribute codes:
//
//	Attribute  Meaning                     Columns used
//	0          start marker                -
//	1          query type                  Flag = type code; Name1/Name2/Expression
//	                                       = target table / remote DB / DB type,
//	                                       connect string and SQL for pass-through,
//	                                       DDL text for data definition
//	2          parameter                   Name1 = name, Flag = data type, LvExtra = size
//	3          select flags                Flag = DISTINCT/TOP/... bits, Name1 = TOP n
//	4          remote database (FROM IN)   Name1 = path, Expression = type
//	5          table / source              Name1 = table, Name2 = alias,
//	                                       Expression = database; UNION parts
//	6          column / expression         Expression, Name1 = alias,
//	                                       Name2 = target column (append/update)
//	7          join                        Name1/Name2 = tables, Flag = 1/2/3
//	                                       (INNER/LEFT/RIGHT), Expression = ON
//	8          WHERE                       Expression
//	9          GROUP BY                    Expression
//	10         HAVING                      Expression
//	11         ORDER BY                    Expression, Name1 = "D" for DESC
//	255        end marker                  -
//
// Rows are taken in table order, as both references do; the Order column is
// not used.

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MSysQueries attribute codes.
const (
	qAttrStart     = 0
	qAttrType      = 1
	qAttrParameter = 2
	qAttrFlag      = 3
	qAttrRemoteDB  = 4
	qAttrTable     = 5
	qAttrColumn    = 6
	qAttrJoin      = 7
	qAttrWhere     = 8
	qAttrGroupBy   = 9
	qAttrHaving    = 10
	qAttrOrderBy   = 11
	qAttrEnd       = 255
)

// Bits on the attribute 3 row.
const (
	qSelectStar     = 0x01
	qDistinct       = 0x02
	qOwnerAccess    = 0x04
	qDistinctRow    = 0x08
	qTop            = 0x10
	qPercent        = 0x20
	qUnionDistinct  = 0x02 // on a UNION query: plain UNION rather than UNION ALL
	qAppendValue    = -0x8000
	qCrosstabPivot  = 0x01
	qCrosstabNormal = 0x02
)

// Names of the two halves of a UNION query on its attribute 5 rows.
const (
	unionPart1 = "X7YZ_____1"
	unionPart2 = "X7YZ_____2"
)

// objectFlagMask keeps the query-type bits of an MSysObjects.Flags value.
const objectFlagMask = 0xF0

// objectFlagHidden marks a hidden object in MSysObjects.Flags.
const objectFlagHidden = 0x08

// QueryType is the kind of a saved query.
type QueryType string

// Query types. The values double as the names used in output.
const (
	QueryTypeSelect         QueryType = "select"
	QueryTypeMakeTable      QueryType = "make-table"
	QueryTypeAppend         QueryType = "append"
	QueryTypeUpdate         QueryType = "update"
	QueryTypeDelete         QueryType = "delete"
	QueryTypeCrosstab       QueryType = "crosstab"
	QueryTypeDataDefinition QueryType = "data-definition"
	QueryTypePassThrough    QueryType = "pass-through"
	QueryTypeUnion          QueryType = "union"
	QueryTypeUnknown        QueryType = "unknown"
)

// queryTypes maps both encodings of a query type: the MSysObjects.Flags bits
// (after objectFlagMask) and the Flag of the attribute 1 row.
var queryTypes = []struct {
	objectFlag int32
	rowFlag    int16
	typ        QueryType
}{
	{0, 1, QueryTypeSelect},
	{80, 2, QueryTypeMakeTable},
	{64, 3, QueryTypeAppend},
	{48, 4, QueryTypeUpdate},
	{32, 5, QueryTypeDelete},
	{16, 6, QueryTypeCrosstab},
	{96, 7, QueryTypeDataDefinition},
	{112, 8, QueryTypePassThrough},
	{128, 9, QueryTypeUnion},
	// dbQSPTBulk: a pass-through query that returns no records
	// (ReturnsRecords = False). Jackcess only names it in a comment; the
	// production frontends use it for EXEC calls, with the same rows as a
	// plain pass-through.
	{144, 10, QueryTypePassThrough},
}

// paramTypeNames maps a parameter's Flag (a Jet column type) to its SQL name.
var paramTypeNames = map[int16]string{
	0:               "Value",
	ColTypeBool:     "Bit",
	ColTypeText:     "Text",
	ColTypeByte:     "Byte",
	ColTypeInt:      "Short",
	ColTypeLong:     "Long",
	ColTypeMoney:    "Currency",
	ColTypeFloat:    "IEEESingle",
	ColTypeDouble:   "IEEEDouble",
	ColTypeDatetime: "DateTime",
	ColTypeBinary:   "Binary",
	ColTypeOLE:      "LongBinary",
	// Not in Jackcess; Memo parameters occur in the production frontends.
	ColTypeMemo: "LongText",
	ColTypeGUID: "Guid",
}

var joinTypeNames = map[int16]string{
	1: " INNER JOIN ",
	2: " LEFT JOIN ",
	3: " RIGHT JOIN ",
}

// QueryRow is one MSysQueries row. Nil string fields were NULL; Jet keeps NULL
// and "" apart, and so does the reconstruction.
type QueryRow struct {
	Attribute  uint8
	Expression *string
	Flag       int16
	Extra      int32
	Name1      *string
	Name2      *string
}

// ReconstructedQuery is the result of rebuilding one saved query.
type ReconstructedQuery struct {
	Type       QueryType
	SQL        string
	Parameters []string
	// Connect is the connect string of a pass-through query, unredacted.
	Connect string
}

// errQueryUnsupported wraps every reason a query cannot be rebuilt.
var errQueryUnsupported = errors.New("query cannot be reconstructed")

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errQueryUnsupported, fmt.Sprintf(format, args...))
}

// queryTypeOf determines the type the way Access and Jackcess do: from the
// object flags, unless they say "select" while the type row says otherwise.
func queryTypeOf(objectFlags int32, rows []QueryRow) QueryType {
	objFlag := objectFlags & objectFlagMask

	if objFlag == 0 {
		if typeRow, ok := firstRow(rows, qAttrType); ok {
			for _, qt := range queryTypes {
				if qt.rowFlag == typeRow.Flag {
					return qt.typ
				}
			}
		}
	}

	for _, qt := range queryTypes {
		if qt.objectFlag == objFlag {
			return qt.typ
		}
	}

	return QueryTypeUnknown
}

// ReconstructQuery rebuilds the SQL of a saved query from its MSysQueries rows
// and its MSysObjects.Flags. It returns an error wrapping errQueryUnsupported,
// with the reason, instead of partial SQL.
func ReconstructQuery(objectFlags int32, rows []QueryRow) (ReconstructedQuery, error) {
	qb := queryBuilder{rows: rows}
	result := ReconstructedQuery{Type: queryTypeOf(objectFlags, rows)}

	if len(rows) == 0 {
		return result, unsupported("no rows in MSysQueries")
	}

	if result.Type == QueryTypeUnknown {
		return result, unsupported("unknown query type (object flags %#x)", objectFlags)
	}

	// The type row must agree with the type we settled on.
	if typeRow, ok := firstRow(rows, qAttrType); ok && !rowFlagMatches(result.Type, typeRow.Flag) {
		return result, unsupported("type row says %d, object flags say %s", typeRow.Flag, result.Type)
	}

	params, err := qb.parameters()
	if err != nil {
		return result, err
	}

	result.Parameters = params

	var sb strings.Builder

	standard := result.Type != QueryTypeDataDefinition && result.Type != QueryTypePassThrough
	if standard && len(params) > 0 {
		sb.WriteString("PARAMETERS " + strings.Join(params, ", ") + ";\n")
	}

	switch result.Type {
	case QueryTypeSelect:
		err = qb.selectSQL(&sb, true, "")
	case QueryTypeMakeTable:
		err = qb.makeTableSQL(&sb)
	case QueryTypeAppend:
		err = qb.appendSQL(&sb)
	case QueryTypeUpdate:
		err = qb.updateSQL(&sb)
	case QueryTypeDelete:
		sb.WriteString("DELETE ")
		err = qb.selectSQL(&sb, false, "")
	case QueryTypeCrosstab:
		err = qb.crosstabSQL(&sb)
	case QueryTypeUnion:
		err = qb.unionSQL(&sb)
	case QueryTypeDataDefinition:
		err = qb.typeRowText(&sb, "DDL")
	case QueryTypePassThrough:
		typeRow, _ := qb.unique(qAttrType)
		result.Connect = str(typeRow.Name1)
		err = qb.typeRowText(&sb, "pass-through SQL")
	case QueryTypeUnknown:
		// Handled above.
	}

	if err != nil {
		return result, err
	}

	if standard {
		owner, ownerErr := qb.hasFlag(qOwnerAccess)
		if ownerErr != nil {
			return result, ownerErr
		}

		if owner {
			sb.WriteString("\nWITH OWNERACCESS OPTION")
		}

		sb.WriteString(";")
	}

	result.SQL = normalizeNewlines(sb.String())

	return result, nil
}

// rowFlagMatches reports whether a type row flag belongs to qt. Pass-through
// has two: plain and bulk.
func rowFlagMatches(qt QueryType, rowFlag int16) bool {
	for _, t := range queryTypes {
		if t.typ == qt && t.rowFlag == rowFlag {
			return true
		}
	}

	return false
}

type queryBuilder struct {
	rows []QueryRow
}

func (qb queryBuilder) byAttr(attr uint8) []QueryRow {
	var out []QueryRow

	for _, row := range qb.rows {
		if row.Attribute == attr {
			out = append(out, row)
		}
	}

	return out
}

// unique returns the single row with attr, a zero row if there is none, and an
// error if there are several.
func (qb queryBuilder) unique(attr uint8) (QueryRow, error) {
	rows := qb.byAttr(attr)

	switch len(rows) {
	case 0:
		return QueryRow{}, nil
	case 1:
		return rows[0], nil
	default:
		return QueryRow{}, unsupported("%d rows with attribute %d, expected at most one", len(rows), attr)
	}
}

func (qb queryBuilder) hasFlag(mask int16) (bool, error) {
	row, err := qb.unique(qAttrFlag)
	if err != nil {
		return false, err
	}

	return row.Flag&mask != 0, nil
}

func (qb queryBuilder) parameters() ([]string, error) {
	var params []string

	for _, row := range qb.byAttr(qAttrParameter) {
		typeName, ok := paramTypeNames[row.Flag]
		if !ok {
			return nil, unsupported("unknown parameter type %d for %s", row.Flag, str(row.Name1))
		}

		param := str(row.Name1) + " " + typeName
		if row.Flag == ColTypeText && row.Extra > 0 {
			param += fmt.Sprintf(" (%d)", row.Extra)
		}

		params = append(params, param)
	}

	return params, nil
}

// selectSQL writes the SELECT part shared by select, make-table, append,
// delete and crosstab queries. into is the " INTO ..." clause of a make-table
// query.
func (qb queryBuilder) selectSQL(sb *strings.Builder, withSelect bool, into string) error {
	return qb.selectSQLWith(sb, withSelect, into, qb.byAttr(qAttrColumn), qb.byAttr(qAttrGroupBy))
}

func (qb queryBuilder) selectSQLWith(sb *strings.Builder, withSelect bool, into string,
	columnRows, groupRows []QueryRow,
) error {
	if withSelect {
		sb.WriteString("SELECT ")

		selectType, err := qb.selectType()
		if err != nil {
			return err
		}

		if selectType != "" {
			sb.WriteString(selectType + " ")
		}
	}

	columns := make([]string, 0, len(columnRows)+1)
	for _, row := range columnRows {
		// Column expressions are stored already quoted.
		columns = append(columns, str(row.Expression)+alias(row.Name1))
	}

	star, err := qb.hasFlag(qSelectStar)
	if err != nil {
		return err
	}

	if star {
		columns = append(columns, "*")
	}

	sb.WriteString(strings.Join(columns, ", "))
	sb.WriteString(into)

	from, err := qb.fromTables()
	if err != nil {
		return err
	}

	if len(from) > 0 {
		sb.WriteString("\nFROM " + strings.Join(from, ", "))

		remote, remoteErr := qb.remoteDB()
		if remoteErr != nil {
			return remoteErr
		}

		sb.WriteString(remote)
	}

	return qb.trailingClauses(sb, groupRows)
}

// trailingClauses writes WHERE, GROUP BY, HAVING and ORDER BY.
func (qb queryBuilder) trailingClauses(sb *strings.Builder, groupRows []QueryRow) error {
	where, err := qb.unique(qAttrWhere)
	if err != nil {
		return err
	}

	if where.Expression != nil {
		sb.WriteString("\nWHERE " + *where.Expression)
	}

	if len(groupRows) > 0 {
		groups := make([]string, 0, len(groupRows))
		for _, row := range groupRows {
			groups = append(groups, str(row.Expression))
		}

		sb.WriteString("\nGROUP BY " + strings.Join(groups, ", "))
	}

	having, err := qb.unique(qAttrHaving)
	if err != nil {
		return err
	}

	if having.Expression != nil {
		sb.WriteString("\nHAVING " + *having.Expression)
	}

	qb.orderBy(sb)

	return nil
}

func (qb queryBuilder) orderBy(sb *strings.Builder) {
	rows := qb.byAttr(qAttrOrderBy)
	if len(rows) == 0 {
		return
	}

	orderings := make([]string, 0, len(rows))

	for _, row := range rows {
		ordering := str(row.Expression)
		if strings.EqualFold(str(row.Name1), "D") {
			ordering += " DESC"
		}

		orderings = append(orderings, ordering)
	}

	sb.WriteString("\nORDER BY " + strings.Join(orderings, ", "))
}

func (qb queryBuilder) selectType() (string, error) {
	row, err := qb.unique(qAttrFlag)
	if err != nil {
		return "", err
	}

	switch {
	case row.Flag&qDistinct != 0:
		return "DISTINCT", nil
	case row.Flag&qDistinctRow != 0:
		return "DISTINCTROW", nil
	case row.Flag&qTop != 0:
		top := "TOP " + str(row.Name1)
		if row.Flag&qPercent != 0 {
			top += " PERCENT"
		}

		return top, nil
	}

	return "", nil
}

func (qb queryBuilder) remoteDB() (string, error) {
	row, err := qb.unique(qAttrRemoteDB)
	if err != nil {
		return "", err
	}

	return remoteDBClause(row.Name1, row.Expression), nil
}

// remoteDBClause renders " IN 'path' [type]". The path is always written,
// even when empty, as long as either part is present.
func remoteDBClause(path, dbType *string) string {
	if path == nil && dbType == nil {
		return ""
	}

	clause := " IN '" + str(path) + "'"
	if dbType != nil {
		clause += " [" + *dbType + "]"
	}

	return clause
}

func (qb queryBuilder) makeTableSQL(sb *strings.Builder) error {
	typeRow, err := qb.unique(qAttrType)
	if err != nil {
		return err
	}

	into := " INTO " + quoteIdentifier(str(typeRow.Name1)) + remoteDBClause(typeRow.Name2, typeRow.Expression)

	return qb.selectSQL(sb, true, into)
}

func (qb queryBuilder) appendSQL(sb *strings.Builder) error {
	typeRow, err := qb.unique(qAttrType)
	if err != nil {
		return err
	}

	sb.WriteString("INSERT INTO " + quoteIdentifier(str(typeRow.Name1)))

	var targets, values, selectCols []QueryRow

	for _, row := range qb.byAttr(qAttrColumn) {
		if row.Name2 != nil {
			targets = append(targets, row)
		}

		if row.Flag&qAppendValue != 0 {
			values = append(values, row)
		} else {
			selectCols = append(selectCols, row)
		}
	}

	if len(targets) > 0 {
		names := make([]string, 0, len(targets))
		for _, row := range targets {
			names = append(names, quoteIdentifier(*row.Name2))
		}

		sb.WriteString(" (" + strings.Join(names, ", ") + ")")
	}

	sb.WriteString(remoteDBClause(typeRow.Name2, typeRow.Expression))
	sb.WriteString("\n")

	if len(values) > 0 {
		exprs := make([]string, 0, len(values))
		for _, row := range values {
			exprs = append(exprs, str(row.Expression))
		}

		sb.WriteString("VALUES (" + strings.Join(exprs, ", ") + ")")

		return nil
	}

	return qb.selectSQLWith(sb, true, "", selectCols, qb.byAttr(qAttrGroupBy))
}

func (qb queryBuilder) updateSQL(sb *strings.Builder) error {
	from, err := qb.fromTables()
	if err != nil {
		return err
	}

	sb.WriteString("UPDATE " + strings.Join(from, ", "))

	remote, err := qb.remoteDB()
	if err != nil {
		return err
	}

	sb.WriteString(remote)

	columnRows := qb.byAttr(qAttrColumn)
	sets := make([]string, 0, len(columnRows))

	for _, row := range columnRows {
		sets = append(sets, quoteIdentifier(str(row.Name2))+" = "+str(row.Expression))
	}

	sb.WriteString("\nSET " + strings.Join(sets, ", "))

	where, err := qb.unique(qAttrWhere)
	if err != nil {
		return err
	}

	if where.Expression != nil {
		sb.WriteString("\nWHERE " + *where.Expression)
	}

	return nil
}

func (qb queryBuilder) crosstabSQL(sb *strings.Builder) error {
	var transform, pivot, normal []QueryRow

	for _, row := range qb.byAttr(qAttrColumn) {
		switch {
		case row.Flag&qCrosstabPivot != 0:
			pivot = append(pivot, row)
		case row.Flag&qCrosstabNormal != 0:
			normal = append(normal, row)
		default:
			transform = append(transform, row)
		}
	}

	if len(transform) > 1 || len(pivot) != 1 {
		return unsupported("crosstab with %d TRANSFORM and %d PIVOT rows", len(transform), len(pivot))
	}

	if len(transform) == 1 && transform[0].Expression != nil {
		sb.WriteString("TRANSFORM " + *transform[0].Expression + alias(transform[0].Name1) + "\n")
	}

	var groups []QueryRow

	for _, row := range qb.byAttr(qAttrGroupBy) {
		if row.Flag&qCrosstabNormal != 0 {
			groups = append(groups, row)
		}
	}

	err := qb.selectSQLWith(sb, true, "", normal, groups)
	if err != nil {
		return err
	}

	sb.WriteString("\nPIVOT " + str(pivot[0].Expression))

	return nil
}

func (qb queryBuilder) unionSQL(sb *strings.Builder) error {
	part := func(id string) (string, error) {
		for _, row := range qb.byAttr(qAttrTable) {
			if str(row.Name2) == id {
				return strings.TrimSpace(str(row.Expression)), nil
			}
		}

		return "", unsupported("UNION part %s missing", id)
	}

	first, err := part(unionPart1)
	if err != nil {
		return err
	}

	second, err := part(unionPart2)
	if err != nil {
		return err
	}

	distinct, err := qb.hasFlag(qUnionDistinct)
	if err != nil {
		return err
	}

	sb.WriteString(first + "\nUNION ")

	if !distinct {
		sb.WriteString("ALL ")
	}

	sb.WriteString(second)
	qb.orderBy(sb)

	return nil
}

func (qb queryBuilder) typeRowText(sb *strings.Builder, what string) error {
	typeRow, err := qb.unique(qAttrType)
	if err != nil {
		return err
	}

	if typeRow.Expression == nil {
		return unsupported("%s missing from the type row", what)
	}

	sb.WriteString(*typeRow.Expression)

	return nil
}

// tableSource is a FROM item: a single table or a join tree.
type tableSource interface {
	render(topLevel bool) (string, error)
	contains(table string) bool
}

type simpleTable struct {
	name string
	expr string
}

func (t *simpleTable) render(bool) (string, error) { return t.expr, nil }

func (t *simpleTable) contains(table string) bool { return strings.EqualFold(t.name, table) }

type joinSource struct {
	from, to tableSource
	joinType int16
	on       []string
}

func (j *joinSource) render(topLevel bool) (string, error) {
	keyword, ok := joinTypeNames[j.joinType]
	if !ok {
		return "", unsupported("unknown join type %d", j.joinType)
	}

	from, err := j.from.render(false)
	if err != nil {
		return "", err
	}

	to, err := j.to.render(false)
	if err != nil {
		return "", err
	}

	on := strings.Join(j.on, ") AND (")
	if len(j.on) > 1 {
		on = "(" + on + ")"
	}

	out := from + keyword + to + " ON " + on
	if !topLevel {
		out = "(" + out + ")"
	}

	return out, nil
}

func (j *joinSource) contains(table string) bool {
	return j.from.contains(table) || j.to.contains(table)
}

// fromTables builds the FROM list: the attribute 5 tables, folded together by
// the attribute 7 joins in row order.
func (qb queryBuilder) fromTables() ([]string, error) {
	sources := make([]tableSource, 0)

	for _, row := range qb.byAttr(qAttrTable) {
		var expr strings.Builder

		if row.Expression != nil {
			expr.WriteString(bracket(*row.Expression) + ".")
		}

		if row.Name1 != nil {
			expr.WriteString(quoteIdentifier(*row.Name1))
		}

		expr.WriteString(alias(row.Name2))

		key := str(row.Name1)
		if row.Name2 != nil {
			key = *row.Name2
		}

		sources = append(sources, &simpleTable{name: key, expr: expr.String()})
	}

	for _, row := range qb.byAttr(qAttrJoin) {
		fromName, toName := str(row.Name1), str(row.Name2)

		var from, to tableSource

		kept := sources[:0]

		for _, src := range sources {
			switch {
			case from == nil && src.contains(fromName):
				from = src
				if to == nil && src.contains(toName) {
					to = src
					kept = append(kept, src)
				}
			case to == nil && src.contains(toName):
				to = src
			default:
				kept = append(kept, src)
			}
		}

		sources = kept

		if from == nil {
			from = &simpleTable{name: fromName, expr: quoteIdentifier(fromName)}
		}

		if to == nil {
			to = &simpleTable{name: toName, expr: quoteIdentifier(toName)}
		}

		if from == to {
			// Another ON condition for a join that already exists. Jackcess
			// prepends it; Access shows the conditions in that order too.
			existing, ok := from.(*joinSource)
			if !ok || existing.joinType != row.Flag {
				return nil, unsupported("inconsistent join between %s and %s", fromName, toName)
			}

			existing.on = append([]string{str(row.Expression)}, existing.on...)

			continue
		}

		sources = append(sources, &joinSource{from: from, to: to, joinType: row.Flag, on: []string{str(row.Expression)}})
	}

	out := make([]string, 0, len(sources))

	for _, src := range sources {
		rendered, err := src.render(true)
		if err != nil {
			return nil, err
		}

		out = append(out, rendered)
	}

	return out, nil
}

// alias renders " AS name", quoting the name when needed.
func alias(name *string) string {
	if name == nil {
		return ""
	}

	return " AS " + quotePart(*name)
}

// quoteIdentifier brackets each dot-separated part of an identifier that needs
// it, e.g. Kunden.Straße Nr → Kunden.[Straße Nr].
func quoteIdentifier(name string) string {
	parts := strings.Split(name, ".")
	for i, part := range parts {
		parts[i] = quotePart(part)
	}

	return strings.Join(parts, ".")
}

// quotePart brackets a name containing anything but letters, digits and
// underscores. Unlike Jackcess, which tests Java's ASCII-only \W, letters
// include umlauts: Access writes Größe, not [Größe].
func quotePart(name string) string {
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return bracket(name)
		}
	}

	return name
}

func bracket(name string) string {
	if len(name) >= 2 && strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		return name
	}

	return "[" + name + "]"
}

func str(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")

	return strings.ReplaceAll(s, "\r", "\n")
}

func firstRow(rows []QueryRow, attr uint8) (QueryRow, bool) {
	for _, row := range rows {
		if row.Attribute == attr {
			return row, true
		}
	}

	return QueryRow{}, false
}
