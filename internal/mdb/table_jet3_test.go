package mdb

import (
	"encoding/binary"
	"os"
	"testing"
)

func writeSyntheticJet3WithTDEF(t *testing.T) string {
	t.Helper()

	path := writeSyntheticDB(t, 2048, 4, JetVersion3)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	pageStart := 2 * 2048
	page := data[pageStart : pageStart+2048]
	page[0] = PageTypeTDEF

	// Minimal single-page Jet3 table definition with two columns.
	binary.LittleEndian.PutUint32(page[tdefLength:], 128)
	binary.LittleEndian.PutUint32(page[tdef3NumRows:], 7)
	page[tdef3TableType] = TableTypeUser
	binary.LittleEndian.PutUint16(page[tdef3NumCols:], 2)
	binary.LittleEndian.PutUint32(page[tdef3NumIdxs:], 0)
	binary.LittleEndian.PutUint32(page[tdef3NumRealIdxs:], 0)

	colStart := tdef3ColsStart

	col0 := page[colStart : colStart+tdef3ColEntrySize]
	col0[col3TypeOff] = ColTypeLong
	col0[col3NumOff] = 0
	binary.LittleEndian.PutUint16(col0[col3OffsetVarOff:], 0)
	col0[col3FlagsOff] = 0x01 // fixed
	binary.LittleEndian.PutUint16(col0[col3OffsetFixOff:], 0)
	binary.LittleEndian.PutUint16(col0[col3LenOff:], 4)

	col1Off := colStart + tdef3ColEntrySize
	col1 := page[col1Off : col1Off+tdef3ColEntrySize]
	col1[col3TypeOff] = ColTypeText
	col1[col3NumOff] = 1
	binary.LittleEndian.PutUint16(col1[col3OffsetVarOff:], 0)
	col1[col3FlagsOff] = 0x02 // nullable variable
	binary.LittleEndian.PutUint16(col1[col3OffsetFixOff:], 0)
	binary.LittleEndian.PutUint16(col1[col3LenOff:], 20)

	nameOff := colStart + 2*tdef3ColEntrySize
	page[nameOff] = 2
	copy(page[nameOff+1:], []byte("Id"))
	nameOff += 3

	page[nameOff] = 4
	copy(page[nameOff+1:], []byte("Name"))

	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

func TestReadTableDefJet3Synthetic(t *testing.T) {
	path := writeSyntheticJet3WithTDEF(t)

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if !db.IsJet3() {
		t.Fatal("expected Jet3 page layout")
	}

	td, err := db.ReadTableDef(2)
	if err != nil {
		t.Fatalf("ReadTableDef: %v", err)
	}

	if td.TableType != TableTypeUser {
		t.Fatalf("TableType = %#x, want %#x", td.TableType, TableTypeUser)
	}

	if td.NumRows != 7 {
		t.Fatalf("NumRows = %d, want 7", td.NumRows)
	}

	if len(td.Columns) != 2 {
		t.Fatalf("Columns = %d, want 2", len(td.Columns))
	}

	if td.Columns[0].Name != "Id" || td.Columns[0].Type != ColTypeLong {
		t.Fatalf("col0 = %+v, want Name=Id Type=Long", td.Columns[0])
	}

	if td.Columns[1].Name != "Name" || td.Columns[1].Type != ColTypeText {
		t.Fatalf("col1 = %+v, want Name=Name Type=Text", td.Columns[1])
	}
}
