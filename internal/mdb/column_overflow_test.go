package mdb

import (
	"encoding/binary"
	"os"
	"testing"
)

// writeSyntheticJet3WithOverflowRow takes the one-row synthetic database and
// moves its row to a new page 3, the way Jet does when a row outgrows its
// page: page 1 keeps only a 4-byte pointer (lookup flag), and the moved row
// carries the delete flag on page 3 so a page scan does not read it twice.
func writeSyntheticJet3WithOverflowRow(t *testing.T) string {
	t.Helper()

	path := writeSyntheticJet3WithOneDataRow(t)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	const pageSize = 2048

	src := data[1*pageSize : 2*pageSize]
	rowStart := int(binary.LittleEndian.Uint16(src[dataRowTableJet3:]) & rowOffsetMask)
	row := append([]byte(nil), src[rowStart:]...)

	// Page 3: a data page of the same table holding the moved row.
	for len(data) < 4*pageSize {
		data = append(data, make([]byte, pageSize)...)
	}

	overflow := data[3*pageSize : 4*pageSize]
	overflow[0] = PageTypeData
	binary.LittleEndian.PutUint32(overflow[dataTDefPage:], 2)
	binary.LittleEndian.PutUint16(overflow[dataNumRowsJet3:], 1)

	movedStart := pageSize - len(row)
	copy(overflow[movedStart:], row)
	binary.LittleEndian.PutUint16(overflow[dataRowTableJet3:], uint16(movedStart)|rowDeleteFlag)

	// Page 1: the original slot now holds row 0 of page 3.
	src = data[1*pageSize : 2*pageSize]
	clear(src[rowStart:])

	pointer := []byte{0, 3, 0, 0} // row 0, page 3
	ptrStart := pageSize - len(pointer)
	copy(src[ptrStart:], pointer)
	binary.LittleEndian.PutUint16(src[dataRowTableJet3:], uint16(ptrStart)|rowLookupFlag)

	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

func TestReadRowsFollowsOverflowPointer(t *testing.T) {
	path := writeSyntheticJet3WithOverflowRow(t)

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	td, err := db.ReadTableDef(2)
	if err != nil {
		t.Fatalf("ReadTableDef: %v", err)
	}

	rows, err := td.ReadRows()
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}

	// Once through the pointer, not a second time from page 3.
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	id, ok := rows[0]["Id"].(int32)
	if !ok || id != 42 {
		t.Fatalf("Id = %#v, want int32(42)", rows[0]["Id"])
	}

	name, ok := rows[0]["Name"].(string)
	if !ok || name != "AB" {
		t.Fatalf("Name = %#v, want %q", rows[0]["Name"], "AB")
	}
}

func TestPageRowBounds(t *testing.T) {
	t.Parallel()

	page := make([]byte, 64)
	binary.LittleEndian.PutUint16(page[dataRowTable:], 60)                 // row 0: 60..64
	binary.LittleEndian.PutUint16(page[dataRowTable+2:], 50|rowLookupFlag) // row 1: 50..60
	copy(page[60:], "AAAA")
	copy(page[50:], "BBBBBBBBBB")

	data, offVal, ok := pageRow(page, dataRowTable, 0)
	if !ok || string(data) != "AAAA" || offVal != 60 {
		t.Errorf("row 0: got %q %#x %v", data, offVal, ok)
	}

	data, offVal, ok = pageRow(page, dataRowTable, 1)
	if !ok || string(data) != "BBBBBBBBBB" || offVal&rowLookupFlag == 0 {
		t.Errorf("row 1: got %q %#x %v", data, offVal, ok)
	}

	_, _, ok = pageRow(page, dataRowTable, 40)
	if ok {
		t.Error("an entry beyond the page must not be returned")
	}
}
