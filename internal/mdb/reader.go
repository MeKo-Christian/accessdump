// Package mdb implements a reader for the Microsoft Jet4 MDB database format.
package mdb

import (
	"crypto/rc4" //nolint:gosec // RC4 only removes the fixed Jet header obfuscation, it protects nothing.
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/text/encoding"
)

const (
	MinPageSize  = 2048
	PageSizeJet4 = 4096
	PageSizeJet3 = 2048
	PageSize     = PageSizeJet4

	// JetVersion3 and related constants are Jet version values at offset 0x14 (little-endian uint32).
	JetVersion3    = 0x00 // Access 97
	JetVersion4    = 0x01 // Access 2000
	JetVersion4x   = 0x02 // Access 2002/2003
	JetVersionACE  = 0x03 // Access 2007
	JetVersionACE4 = 0x04 // Access 2010
	JetVersionACE5 = 0x05 // Access 2013
	JetVersionACE6 = 0x06 // Access 2016+

	// PageTypeDB and related constants are page types (byte 0 of each page).
	PageTypeDB    = 0x00
	PageTypeData  = 0x01
	PageTypeTDEF  = 0x02
	PageTypeIIdx  = 0x03
	PageTypeLIdx  = 0x04
	PageTypeUsage = 0x05

	// Header offsets.
	offsetMagic      = 0x00
	offsetDBName     = 0x04
	offsetJetVersion = 0x14
	offsetCodePage   = 0x3C
	offsetDBKey      = 0x3E
	offsetSortOrder  = 0x6E

	// The header from offsetHeaderCrypt onward is XORed with an RC4 keystream
	// derived from a fixed key: 126 bytes in Jet 3, 128 bytes later on.
	offsetHeaderCrypt = 0x18
	headerCryptJet3   = 126
	headerCryptJet4   = 128
)

var (
	magicBytes     = [4]byte{0x00, 0x01, 0x00, 0x00}
	headerCryptKey = []byte{0xC7, 0xDA, 0x39, 0x6B}
)

var (
	ErrJet3TableLayoutUnsupported = errors.New("mdb: Jet 3.5 table layout parsing is not implemented")
	ErrJet3RowLayoutUnsupported   = errors.New("mdb: Jet 3.5 row layout parsing is not implemented")
	ErrJet3LvalLayoutUnsupported  = errors.New("mdb: Jet 3.5 LVAL/MEMO parsing is not implemented")
)

// Header holds parsed database header fields from page 0.
type Header struct {
	JetVersion uint32
	DBName     string
	CodePage   uint16
	DBKey      uint32
	SortOrder  uint32
}

// Database represents an open MDB file.
type Database struct {
	f         *os.File
	Header    Header
	pageSize  int64
	pageCount int64
	// charset decodes Jet 3 text, which is stored in the database code page.
	charset encoding.Encoding
}

// Open opens an MDB file and parses its header.
func Open(path string) (*Database, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mdb: open: %w", err)
	}

	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mdb: stat: %w", err)
	}

	if fi.Size() < MinPageSize {
		f.Close()
		return nil, fmt.Errorf("mdb: file too small (%d bytes)", fi.Size())
	}

	db := &Database{
		f:        f,
		pageSize: PageSizeJet4,
	}

	err = db.parseHeader()
	if err != nil {
		f.Close()
		return nil, err
	}

	db.pageSize = detectPageSize(f, fi.Size(), db.Header.JetVersion)
	if fi.Size() < db.pageSize {
		f.Close()
		return nil, fmt.Errorf("mdb: file too small for page size %d (%d bytes)", db.pageSize, fi.Size())
	}

	db.pageCount = fi.Size() / db.pageSize
	db.charset = codePageEncoding(db.Header.CodePage)

	return db, nil
}

// Close closes the database file.
func (db *Database) Close() error {
	return db.f.Close()
}

// PageCount returns the total number of pages in the database.
func (db *Database) PageCount() int64 {
	return db.pageCount
}

// PageSize returns the database page size in bytes.
func (db *Database) PageSize() int64 {
	return db.pageSize
}

// IsJet4 returns true if the database uses Jet4 or later format.
func (db *Database) IsJet4() bool {
	return db.pageSize == PageSizeJet4
}

// IsJet3 returns true if the database uses Jet 3.x page layout.
func (db *Database) IsJet3() bool {
	return db.pageSize == PageSizeJet3
}

// DataPageLayoutOffsets returns (numRowsOffset, rowTableOffset) for the
// current database page layout.
func (db *Database) DataPageLayoutOffsets() (int, int) {
	if db != nil && db.IsJet3() {
		return dataNumRowsJet3, dataRowTableJet3
	}

	return dataNumRows, dataRowTable
}

// ReadPage reads a single page by page number.
func (db *Database) ReadPage(pageNum int64) ([]byte, error) {
	if pageNum < 0 || pageNum >= db.pageCount {
		return nil, fmt.Errorf("mdb: page %d out of range (0..%d)", pageNum, db.pageCount-1)
	}

	page := make([]byte, db.pageSize)

	_, err := db.f.ReadAt(page, pageNum*db.pageSize)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("mdb: read page %d: %w", pageNum, err)
	}

	return page, nil
}

func (db *Database) parseHeader() error {
	page := make([]byte, MinPageSize)

	_, err := db.f.ReadAt(page, 0)
	if err != nil {
		return fmt.Errorf("mdb: read header: %w", err)
	}

	// Validate magic bytes.
	var magic [4]byte
	copy(magic[:], page[offsetMagic:offsetMagic+4])

	if magic != magicBytes {
		return fmt.Errorf("mdb: invalid magic bytes: %x", magic)
	}

	// DB name/identifier string (null-terminated, up to 16 bytes).
	nameEnd := offsetDBName
	for nameEnd < offsetDBName+16 && page[nameEnd] != 0 {
		nameEnd++
	}

	db.Header.DBName = string(page[offsetDBName:nameEnd])

	db.Header.JetVersion = binary.LittleEndian.Uint32(page[offsetJetVersion:])

	err = decryptHeader(page, db.Header.JetVersion)
	if err != nil {
		return err
	}

	db.Header.CodePage = binary.LittleEndian.Uint16(page[offsetCodePage:])
	db.Header.DBKey = binary.LittleEndian.Uint32(page[offsetDBKey:])
	db.Header.SortOrder = binary.LittleEndian.Uint32(page[offsetSortOrder:])

	return nil
}

// decryptHeader removes the RC4 obfuscation from the header fields that
// follow the Jet version, in place.
func decryptHeader(page []byte, jetVersion uint32) error {
	n := headerCryptJet4
	if jetVersion == JetVersion3 {
		n = headerCryptJet3
	}

	cipher, err := rc4.NewCipher(headerCryptKey) //nolint:gosec // see import
	if err != nil {
		return fmt.Errorf("mdb: header cipher: %w", err)
	}

	region := page[offsetHeaderCrypt : offsetHeaderCrypt+n]
	cipher.XORKeyStream(region, region)

	return nil
}

func detectPageSize(f *os.File, fileSize int64, jetVersion uint32) int64 {
	candidates := []int64{PageSizeJet4, PageSizeJet3}
	if jetVersion == JetVersion3 {
		candidates = []int64{PageSizeJet3, PageSizeJet4}
	}

	for _, candidate := range candidates {
		if looksLikePageLayout(f, fileSize, candidate) {
			return candidate
		}
	}

	// Fallback to version when layout probing is inconclusive.
	if jetVersion == JetVersion3 {
		return PageSizeJet3
	}

	if fileSize%PageSizeJet4 == 0 {
		return PageSizeJet4
	}

	return PageSizeJet3
}

func looksLikePageLayout(f *os.File, fileSize int64, pageSize int64) bool {
	if fileSize < pageSize*3 {
		return false
	}

	// A valid database should have MSysObjects TDEF on page 2.
	page2Type := make([]byte, 1)

	_, err := f.ReadAt(page2Type, 2*pageSize)
	if err != nil && err != io.EOF {
		return false
	}

	if page2Type[0] != PageTypeTDEF {
		return false
	}

	page1Type := make([]byte, 1)

	_, err = f.ReadAt(page1Type, pageSize)
	if err != nil && err != io.EOF {
		return false
	}

	return isKnownPageType(page1Type[0])
}

func isKnownPageType(t byte) bool {
	switch t {
	case PageTypeDB, PageTypeData, PageTypeTDEF, PageTypeIIdx, PageTypeLIdx, PageTypeUsage:
		return true
	default:
		return false
	}
}

// PageType returns the type byte of a page.
func PageType(page []byte) byte {
	return page[0]
}
