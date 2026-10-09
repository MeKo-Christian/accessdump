package extract

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/MeKo-Christian/accessdump/internal/mdb"
)

// QueryType identifies the kind of a saved query.
type QueryType string

// Query types.
const (
	QueryTypeSelect         QueryType = QueryType(mdb.QueryTypeSelect)
	QueryTypeMakeTable      QueryType = QueryType(mdb.QueryTypeMakeTable)
	QueryTypeAppend         QueryType = QueryType(mdb.QueryTypeAppend)
	QueryTypeUpdate         QueryType = QueryType(mdb.QueryTypeUpdate)
	QueryTypeDelete         QueryType = QueryType(mdb.QueryTypeDelete)
	QueryTypeCrosstab       QueryType = QueryType(mdb.QueryTypeCrosstab)
	QueryTypeDataDefinition QueryType = QueryType(mdb.QueryTypeDataDefinition)
	QueryTypePassThrough    QueryType = QueryType(mdb.QueryTypePassThrough)
	QueryTypeUnion          QueryType = QueryType(mdb.QueryTypeUnion)
	QueryTypeUnknown        QueryType = QueryType(mdb.QueryTypeUnknown)
)

// SQLStatus says whether a query's SQL could be reconstructed.
type SQLStatus string

const (
	// SQLStatusFound means SQL holds the complete reconstructed statement.
	SQLStatusFound SQLStatus = SQLStatus(mdb.SQLStatusFound)
	// SQLStatusTableMissing means MSysQueries could not be read at all.
	SQLStatusTableMissing SQLStatus = SQLStatus(mdb.SQLStatusTableMissing)
	// SQLStatusNotInTable means MSysQueries holds no rows for the query.
	SQLStatusNotInTable SQLStatus = SQLStatus(mdb.SQLStatusNotInTable)
	// SQLStatusUnsupported means the rows could not be turned into SQL;
	// Query.Reason says why.
	SQLStatusUnsupported SQLStatus = SQLStatus(mdb.SQLStatusUnsupported)
)

// OwnerKind is the kind of object an embedded query belongs to.
type OwnerKind string

const (
	OwnerForm    OwnerKind = "form"
	OwnerReport  OwnerKind = "report"
	OwnerUnknown OwnerKind = "unknown"
)

// QueryOwner identifies the form or report, and possibly the control on it,
// that an embedded query is the record or row source of.
type QueryOwner struct {
	Kind   OwnerKind
	Object string
	// Control is empty for the record source of the form or report itself.
	Control string
}

// Query is one saved query.
type Query struct {
	// Name is the name in MSysObjects, e.g. "QRY_Kalkulation" or, for an
	// embedded query, "~sq_cAuftrag~sq_cKunde".
	Name string
	Type QueryType
	// SQL is the complete statement, or empty when SQLStatus is not
	// SQLStatusFound. Lines end in LF. Passwords are redacted.
	SQL string
	// Parameters lists the PARAMETERS declarations, e.g. "[Ab Datum] DateTime".
	Parameters []string
	// Connect is the connect string of a pass-through query, passwords redacted.
	Connect string
	// Embedded is true for the ~sq_ queries Access keeps for record and row
	// sources typed directly into a form, report or control.
	Embedded bool
	// Owner is set for embedded queries.
	Owner  *QueryOwner
	Hidden bool

	SQLStatus SQLStatus
	// Reason explains a status other than SQLStatusFound.
	Reason string
}

// Queries opens an Access database file and returns all saved queries,
// embedded ones included, sorted by name. A query whose SQL cannot be
// reconstructed is still returned, with SQLStatus and Reason saying why.
//
// Pass a *slog.Logger to receive debug messages, or nil for slog.Default().
func Queries(path string, log *slog.Logger) ([]Query, error) {
	if log == nil {
		log = slog.Default()
	}

	db, err := mdb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer db.Close()

	cpErr := db.CodePageErr()
	if cpErr != nil {
		log.Warn("query text may be incomplete", "file", path, "err", cpErr)
	}

	defs, err := db.ReadQueries()
	if err != nil {
		return nil, fmt.Errorf("read queries %q: %w", path, err)
	}

	out := make([]Query, 0, len(defs))

	for _, def := range defs {
		q := Query{
			Name:       def.Name,
			Type:       QueryType(def.Type),
			SQL:        RedactPasswords(def.SQL),
			Parameters: def.Parameters,
			Connect:    RedactPasswords(def.Connect),
			Hidden:     def.Hidden,
			SQLStatus:  SQLStatus(def.SQLStatus),
			Reason:     def.Reason,
		}

		if owner, ok := ParseEmbeddedQueryName(def.Name); ok {
			q.Embedded = true
			q.Owner = &owner
		}

		if q.SQLStatus != SQLStatusFound {
			log.Debug("query not reconstructed", "path", path, "query", q.Name, "status", q.SQLStatus, "reason", q.Reason)
		}

		out = append(out, q)
	}

	return out, nil
}

// embeddedPrefix starts the name of every embedded query.
const embeddedPrefix = "~sq_"

// ParseEmbeddedQueryName reports whether name belongs to an embedded query and,
// if so, which object it belongs to:
//
//	~sq_f<Form>                 record source of a form
//	~sq_r<Report>               record source of a report
//	~sq_c<Form>~sq_c<Control>   row source of a control on a form
//	~sq_d<Report>~sq_d<Control> row source of a control on a report
func ParseEmbeddedQueryName(name string) (QueryOwner, bool) {
	if !strings.HasPrefix(name, embeddedPrefix) || len(name) <= len(embeddedPrefix) {
		return QueryOwner{}, false
	}

	marker := name[len(embeddedPrefix)]
	rest := name[len(embeddedPrefix)+1:]

	switch marker {
	case 'f':
		return QueryOwner{Kind: OwnerForm, Object: rest}, true
	case 'r':
		return QueryOwner{Kind: OwnerReport, Object: rest}, true
	case 'c', 'd':
		kind := OwnerForm
		if marker == 'd' {
			kind = OwnerReport
		}

		object, control, found := strings.Cut(rest, embeddedPrefix+string(marker))
		if !found {
			return QueryOwner{Kind: kind, Object: rest}, true
		}

		return QueryOwner{Kind: kind, Object: object, Control: control}, true
	default:
		return QueryOwner{Kind: OwnerUnknown, Object: rest}, true
	}
}

// passwordPattern matches the value of PWD= or Password= in a connect string,
// either in braces (ODBC quoting, may contain ";", a literal "}" is written
// "}}") or up to the next separator. A connect string can also sit inside SQL, e.g. in
// IN ” [ODBC;...;PWD=x], hence the quote and bracket terminators.
var passwordPattern = regexp.MustCompile(`(?i)\b(PWD|PASSWORD)(\s*=\s*)(\{(?:[^}]|\}\})*\}|[^;'"\]\r\n]*)`)

// RedactPasswords replaces every PWD=/Password= value in s with ***.
func RedactPasswords(s string) string {
	return passwordPattern.ReplaceAllString(s, "${1}${2}***")
}
