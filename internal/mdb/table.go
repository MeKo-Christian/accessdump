package mdb

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// Jet4 TDEF offsets.
const (
	tdefNextPage      = 0x04
	tdefLength        = 0x08
	tdefNumRows       = 0x10
	tdefAutoNum       = 0x14
	tdefMaxCols       = 0x19
	tdefNumVarCols    = 0x1B
	tdefTableType     = 0x28
	tdefNumCols       = 0x2D
	tdefNumIdxs       = 0x2F
	tdefNumRealIdxs   = 0x33
	tdefUsedPagesPtr  = 0x37
	tdefFreePagesPtr  = 0x3B
	tdefColsStart     = 0x3F
	tdefRIdxEntrySize = 12
	tdefColEntrySize  = 25
)

// Jet3 TDEF offsets and entry sizes.
const (
	tdef3NumRows       = 0x0C
	tdef3TableType     = 0x14
	tdef3NumCols       = 0x19
	tdef3NumIdxs       = 0x1B
	tdef3NumRealIdxs   = 0x1F
	tdef3ColsStart     = 0x2B
	tdef3RIdxEntrySize = 8
	tdef3ColEntrySize  = 18
)

// Jet3 column definition offsets within an 18-byte entry.
const (
	col3TypeOff      = 0
	col3NumOff       = 1
	col3OffsetVarOff = 3
	col3ScaleOff     = 12
	col3PrecOff      = 11
	col3FlagsOff     = 13
	col3OffsetFixOff = 14
	col3LenOff       = 16
)

// Column definition offsets within a 25-byte entry.
const (
	colTypeOff      = 0
	colNumOff       = 5
	colOffsetVarOff = 7
	colScaleOff     = 11
	colPrecOff      = 12
	colFlagsOff     = 15
	colOffsetFixOff = 21
	colLenOff       = 23
)

// Column types.
const (
	ColTypeBool     = 0x01
	ColTypeByte     = 0x02
	ColTypeInt      = 0x03
	ColTypeLong     = 0x04
	ColTypeMoney    = 0x05
	ColTypeFloat    = 0x06
	ColTypeDouble   = 0x07
	ColTypeDatetime = 0x08
	ColTypeBinary   = 0x09
	ColTypeText     = 0x0A
	ColTypeOLE      = 0x0B
	ColTypeMemo     = 0x0C
	ColTypeGUID     = 0x0F
	ColTypeNumeric  = 0x10
)

// Table type constants.
const (
	TableTypeUser   = 0x4E
	TableTypeSystem = 0x53
)

// Column represents a parsed column definition.
type Column struct {
	Name      string
	Type      byte
	ColNum    uint16
	OffsetVar uint16 // index in variable-length offset table
	OffsetFix uint16 // offset within fixed data area
	Length    uint16
	Flags     byte
	Scale     byte
	Precision byte
}

// IsFixed returns true if this column stores fixed-length data.
func (c *Column) IsFixed() bool {
	return c.Flags&0x01 != 0
}

// IsNullable returns true if this column allows NULLs.
func (c *Column) IsNullable() bool {
	return c.Flags&0x02 != 0
}

// IsLongValue returns true if the column uses LVAL pages (MEMO or OLE).
func (c *Column) IsLongValue() bool {
	return c.Type == ColTypeMemo || c.Type == ColTypeOLE
}

// TableDef represents a parsed table definition.
type TableDef struct {
	Name         string
	DefPage      int64 // page number of the TDEF
	NumRows      uint32
	TableType    byte
	Columns      []*Column
	NumIdxs      uint32
	NumRealIdxs  uint32
	UsedPagesMap []byte // raw usage map data
	FreePagesMap []byte // raw usage map data

	db *Database
}

// ReadTableDef reads and parses a table definition from the given TDEF page.
func (db *Database) ReadTableDef(tdefPage int64) (*TableDef, error) {
	if db.IsJet3() {
		return db.readTableDefJet3(tdefPage)
	}

	// Read the full TDEF, which may span multiple pages.
	tdefData, err := db.readTDEFPages(tdefPage)
	if err != nil {
		return nil, err
	}

	if len(tdefData) < tdefColsStart {
		return nil, fmt.Errorf("mdb: TDEF at page %d too short (%d bytes)", tdefPage, len(tdefData))
	}

	td := &TableDef{
		DefPage:     tdefPage,
		NumRows:     binary.LittleEndian.Uint32(tdefData[tdefNumRows:]),
		TableType:   tdefData[tdefTableType],
		NumIdxs:     binary.LittleEndian.Uint32(tdefData[tdefNumIdxs:]),
		NumRealIdxs: binary.LittleEndian.Uint32(tdefData[tdefNumRealIdxs:]),
		db:          db,
	}

	numCols := int(binary.LittleEndian.Uint16(tdefData[tdefNumCols:]))

	// Skip real index entries to reach column definitions.
	colStart := tdefColsStart + int(td.NumRealIdxs)*tdefRIdxEntrySize
	if colStart+numCols*tdefColEntrySize > len(tdefData) {
		return nil, fmt.Errorf("mdb: TDEF at page %d: column data extends past TDEF (%d cols, colStart=%d, tdefLen=%d)",
			tdefPage, numCols, colStart, len(tdefData))
	}

	// Parse column definitions.
	td.Columns = make([]*Column, numCols)
	for i := range numCols {
		off := colStart + i*tdefColEntrySize
		entry := tdefData[off : off+tdefColEntrySize]
		td.Columns[i] = &Column{
			Type:      entry[colTypeOff],
			ColNum:    binary.LittleEndian.Uint16(entry[colNumOff:]),
			OffsetVar: binary.LittleEndian.Uint16(entry[colOffsetVarOff:]),
			Scale:     entry[colScaleOff],
			Precision: entry[colPrecOff],
			Flags:     entry[colFlagsOff],
			OffsetFix: binary.LittleEndian.Uint16(entry[colOffsetFixOff:]),
			Length:    binary.LittleEndian.Uint16(entry[colLenOff:]),
		}
	}

	// Parse column names (UCS-2LE in Jet4: 2-byte length prefix + UCS-2 data).
	nameOff := colStart + numCols*tdefColEntrySize
	for i := range numCols {
		if nameOff+2 > len(tdefData) {
			return nil, fmt.Errorf("mdb: TDEF at page %d: name data truncated at column %d", tdefPage, i)
		}

		nameLen := int(binary.LittleEndian.Uint16(tdefData[nameOff:]))

		nameOff += 2
		if nameOff+nameLen > len(tdefData) {
			return nil, fmt.Errorf("mdb: TDEF at page %d: name %d extends past TDEF", tdefPage, i)
		}

		td.Columns[i].Name = decodeUCS2(tdefData[nameOff : nameOff+nameLen])
		nameOff += nameLen
	}

	// Parse usage maps (after column names + index data).
	// The usage maps are after the index definitions which follow column names.
	// For now, we'll locate them when we need to iterate data pages.

	return td, nil
}

func (db *Database) readTableDefJet3(tdefPage int64) (*TableDef, error) {
	tdefData, err := db.readTDEFPages(tdefPage)
	if err != nil {
		return nil, err
	}

	if len(tdefData) < tdef3ColsStart {
		return nil, fmt.Errorf("mdb: Jet3 TDEF at page %d too short (%d bytes)", tdefPage, len(tdefData))
	}

	td := &TableDef{
		DefPage:     tdefPage,
		NumRows:     binary.LittleEndian.Uint32(tdefData[tdef3NumRows:]),
		TableType:   tdefData[tdef3TableType],
		NumIdxs:     binary.LittleEndian.Uint32(tdefData[tdef3NumIdxs:]),
		NumRealIdxs: binary.LittleEndian.Uint32(tdefData[tdef3NumRealIdxs:]),
		db:          db,
	}

	numCols := int(binary.LittleEndian.Uint16(tdefData[tdef3NumCols:]))
	if numCols <= 0 {
		return nil, fmt.Errorf("mdb: Jet3 TDEF at page %d has invalid column count %d", tdefPage, numCols)
	}

	colStart := tdef3ColsStart + int(td.NumRealIdxs)*tdef3RIdxEntrySize
	if colStart+numCols*tdef3ColEntrySize > len(tdefData) {
		return nil, fmt.Errorf("mdb: Jet3 TDEF at page %d: column data extends past TDEF (%d cols, colStart=%d, tdefLen=%d)",
			tdefPage, numCols, colStart, len(tdefData))
	}

	td.Columns = make([]*Column, numCols)
	for i := range numCols {
		off := colStart + i*tdef3ColEntrySize
		entry := tdefData[off : off+tdef3ColEntrySize]

		td.Columns[i] = &Column{
			Type:      entry[col3TypeOff],
			ColNum:    uint16(entry[col3NumOff]),
			OffsetVar: binary.LittleEndian.Uint16(entry[col3OffsetVarOff:]),
			Scale:     entry[col3ScaleOff],
			Precision: entry[col3PrecOff],
			Flags:     entry[col3FlagsOff],
			OffsetFix: binary.LittleEndian.Uint16(entry[col3OffsetFixOff:]),
			Length:    binary.LittleEndian.Uint16(entry[col3LenOff:]),
		}
	}

	// Jet3 names are stored with a 1-byte byte-length prefix.
	nameOff := colStart + numCols*tdef3ColEntrySize
	for i := range numCols {
		if nameOff+1 > len(tdefData) {
			return nil, fmt.Errorf("mdb: Jet3 TDEF at page %d: name data truncated at column %d", tdefPage, i)
		}

		nameLen := int(tdefData[nameOff])
		nameOff++

		if nameOff+nameLen > len(tdefData) {
			return nil, fmt.Errorf("mdb: Jet3 TDEF at page %d: name %d extends past TDEF", tdefPage, i)
		}

		td.Columns[i].Name = db.decodeJet3Text(tdefData[nameOff : nameOff+nameLen])
		nameOff += nameLen
	}

	return td, nil
}

// readTDEFPages reads the complete table definition by following the next-page chain.
func (db *Database) readTDEFPages(startPage int64) ([]byte, error) {
	page, err := db.ReadPage(startPage)
	if err != nil {
		return nil, err
	}

	if PageType(page) != PageTypeTDEF {
		return nil, fmt.Errorf("mdb: page %d is type %#x, not TDEF (%#x)", startPage, PageType(page), PageTypeTDEF)
	}

	tdefLen := int(binary.LittleEndian.Uint32(page[tdefLength:]))

	// First TDEF page contributes bytes starting from offset 8 (after page header type+unknown+freeSpace+nextPage).
	// Actually the full TDEF starts at byte 0 of the page (the page type IS part of tdef header).
	// For multi-page TDEFs, subsequent pages contribute from offset 8 onward.
	result := make([]byte, 0, tdefLen)
	result = append(result, page...)

	nextPage := binary.LittleEndian.Uint32(page[tdefNextPage:])
	for nextPage != 0 {
		np, err := db.ReadPage(int64(nextPage))
		if err != nil {
			return nil, fmt.Errorf("mdb: TDEF continuation page %d: %w", nextPage, err)
		}
		// Continuation pages contribute from offset 8 onward.
		result = append(result, np[8:]...)
		nextPage = binary.LittleEndian.Uint32(np[tdefNextPage:])
	}

	return result, nil
}

// decodeUCS2 decodes a UCS-2LE byte slice to a Go string.
func decodeUCS2(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}

	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}

	return string(utf16.Decode(u16))
}

// ColTypeName returns a human-readable name for a column type byte.
func ColTypeName(t byte) string {
	switch t {
	case ColTypeBool:
		return "Bool"
	case ColTypeByte:
		return "Byte"
	case ColTypeInt:
		return "Int"
	case ColTypeLong:
		return "Long"
	case ColTypeMoney:
		return "Money"
	case ColTypeFloat:
		return "Float"
	case ColTypeDouble:
		return "Double"
	case ColTypeDatetime:
		return "DateTime"
	case ColTypeBinary:
		return "Binary"
	case ColTypeText:
		return "Text"
	case ColTypeOLE:
		return "OLE"
	case ColTypeMemo:
		return "Memo"
	case ColTypeGUID:
		return "GUID"
	case ColTypeNumeric:
		return "Numeric"
	default:
		return fmt.Sprintf("Unknown(%#x)", t)
	}
}
