package mdb

import (
	"encoding/binary"
	"os"
	"testing"
)

const testMDB = "../../testdata/sample.mdb"

func testDB(t *testing.T) *Database {
	t.Helper()

	_, err := os.Stat(testMDB)
	if os.IsNotExist(err) {
		t.Skip("testdata/sample.mdb not available")
	}

	db, err := Open(testMDB)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

const startMDB = "../../testdata/Start.mdb"

func startDB(t *testing.T) *Database {
	t.Helper()

	_, err := os.Stat(startMDB)
	if os.IsNotExist(err) {
		t.Skip("testdata/Start.mdb not available (proprietary fixture)")
	}

	db, err := Open(startMDB)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

func TestOpenAndHeader(t *testing.T) {
	db := testDB(t)

	if !db.IsJet4() {
		t.Error("IsJet4() = false, want true")
	}

	// sample.mdb: 71 pages (290816 / 4096).
	if db.PageCount() != 71 {
		t.Errorf("PageCount = %d, want 71", db.PageCount())
	}

	// The code page is only readable once the header obfuscation is removed.
	if db.Header.CodePage != 1250 {
		t.Errorf("CodePage = %d, want 1250", db.Header.CodePage)
	}
}

func TestOpenStartHeaderCodePage(t *testing.T) {
	db := startDB(t)

	if db.Header.CodePage != 1252 {
		t.Errorf("CodePage = %d, want 1252", db.Header.CodePage)
	}
}

func TestDecryptHeaderCodePage(t *testing.T) {
	for _, jetVersion := range []uint32{JetVersion3, JetVersion4} {
		page := make([]byte, MinPageSize)
		binary.LittleEndian.PutUint16(page[offsetCodePage:], 1252)

		// The obfuscation is a plain XOR keystream, so applying it twice
		// restores the input; the first call produces what Access writes.
		for range 2 {
			err := decryptHeader(page, jetVersion)
			if err != nil {
				t.Fatalf("decryptHeader: %v", err)
			}

			if jetVersion == JetVersion3 && page[offsetHeaderCrypt+headerCryptJet3] != 0 {
				t.Fatal("Jet 3 decryption touched bytes past the 126-byte region")
			}
		}

		if got := binary.LittleEndian.Uint16(page[offsetCodePage:]); got != 1252 {
			t.Errorf("jet version %d: CodePage = %d, want 1252", jetVersion, got)
		}
	}
}

func TestReadPage(t *testing.T) {
	db := testDB(t)

	// Page 0 should be DB definition page.
	page0, err := db.ReadPage(0)
	if err != nil {
		t.Fatalf("ReadPage(0): %v", err)
	}

	if PageType(page0) != PageTypeDB {
		t.Errorf("Page 0 type = %#x, want %#x", PageType(page0), PageTypeDB)
	}

	// Page 2 should be TDEF (MSysObjects).
	page2, err := db.ReadPage(2)
	if err != nil {
		t.Fatalf("ReadPage(2): %v", err)
	}

	if PageType(page2) != PageTypeTDEF {
		t.Errorf("Page 2 type = %#x, want %#x (TDEF)", PageType(page2), PageTypeTDEF)
	}

	// Out of range.
	_, err = db.ReadPage(10000)
	if err == nil {
		t.Error("ReadPage(10000) should fail")
	}
}

func TestOpenInvalidFile(t *testing.T) {
	// Non-existent file.
	_, err := Open("/tmp/nonexistent.mdb")
	if err == nil {
		t.Error("Open non-existent should fail")
	}

	// Create a too-small file.
	tmp, err := os.CreateTemp("", "tiny*.mdb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	_, err = tmp.WriteString("tiny")
	if err != nil {
		t.Fatal(err)
	}

	err = tmp.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = Open(tmp.Name())
	if err == nil {
		t.Error("Open tiny file should fail")
	}
}
