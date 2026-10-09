package mdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// EmbeddedImage represents an image found inside a form blob.
type EmbeddedImage struct {
	FormName string // Access form name (e.g. "Startschirm")
	FileName string // original filename if found in blob metadata
	Format   string // "jpeg", "png", "bmp", or "gif"
	Data     []byte // raw image bytes
}

// Image format magic bytes.
var (
	sigJPEG    = []byte{0xFF, 0xD8, 0xFF}
	sigPNG     = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	sigBMP     = []byte{0x42, 0x4D}
	sigGIF87a  = []byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}
	sigGIF89a  = []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}
	sigJPEGEnd = []byte{0xFF, 0xD9}

	// Access form blob image record header: property 0x018E, type 0x0B (image),
	// flags 0xFFFF, followed by a 4-byte LE image data size.
	// Pattern: 8e 01 00 3e 02 0b 00 0xFF [size LE32]
	// We match the tail: 3e 02 0b 00 00 00 00 00 and two trailing 0xFF bytes.
	sigImageRecordTail = []byte{0x3e, 0x02, 0x0b, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff}
)

// ExtractImages finds all embedded images in form blobs from MSysAccessStorage.
// It returns one EmbeddedImage per image found. Images that are byte-identical
// and have the same resolved filename are deduplicated.
func ExtractImages(db *Database) ([]EmbeddedImage, error) {
	tree, err := loadFormStorageTree(db)
	if err != nil {
		return nil, err
	}

	formNames := parseFormDirData(tree)

	var images []EmbeddedImage

	for folderName, children := range tree.children {
		formName := formNames[folderName]
		if formName == "" {
			formName = "Form_" + folderName
		}

		for _, child := range children {
			if child.name != "Blob" || len(child.data) < 16 {
				continue
			}

			found := scanBlobForImages(child.data, formName)
			images = append(images, found...)
		}
	}

	return deduplicateImages(images), nil
}

// deduplicateImages removes images that are byte-identical and have the same
// base filename (without extension). Two images on different forms are
// considered duplicates only when both their FileName stems match and their
// Data is identical. Images without a FileName are never deduplicated.
func deduplicateImages(images []EmbeddedImage) []EmbeddedImage {
	type dedupeKey struct {
		nameStem string // filename without extension
		size     int
		hash     uint64 // FNV-style fast hash of content
	}

	seen := map[dedupeKey]bool{}
	var result []EmbeddedImage

	for _, img := range images {
		if img.FileName == "" {
			result = append(result, img)
			continue
		}

		stem := fileNameStem(img.FileName)
		key := dedupeKey{
			nameStem: stem,
			size:     len(img.Data),
			hash:     fnvHash(img.Data),
		}

		if !seen[key] {
			result = append(result, img)
			seen[key] = true
		}
	}

	return result
}

// fileNameStem returns a filename without its extension.
func fileNameStem(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[:i]
		}
	}

	return name
}

func fnvHash(data []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range data {
		h ^= uint64(b)
		h *= 1099511628211
	}

	return h
}

// formStorageTree is a lightweight structure for form blobs only.
type formStorageTree struct {
	// children maps folder name (e.g. "0", "2") to its child nodes.
	children map[string][]formNode
	// dirData is the raw \x03DirData stream.
	dirData []byte
}

type formNode struct {
	name string
	data []byte
}

// loadFormStorageTree reads MSysAccessStorage and extracts only the Forms subtree.
func loadFormStorageTree(db *Database) (*formStorageTree, error) {
	td, err := findTable(db, "MSysAccessStorage")
	if err != nil {
		return nil, fmt.Errorf("image: %w", err)
	}

	rows, err := td.ReadRows()
	if err != nil {
		return nil, fmt.Errorf("image: read MSysAccessStorage: %w", err)
	}

	// Build a simple ID->row index and find the Forms folder.
	type entry struct {
		id       int32
		parentID int32
		name     string
		lvRaw    []byte
	}

	var entries []entry
	idToEntry := map[int32]*entry{}
	rootID := int32(0)
	formsID := int32(0)

	for _, row := range rows {
		e := entry{}
		if v, ok := row["Id"].(int32); ok {
			e.id = v
		}

		if v, ok := row["ParentId"].(int32); ok {
			e.parentID = v
		}

		if v, ok := row["Name"].(string); ok {
			e.name = v
		}

		if v, ok := row["Lv"].([]byte); ok {
			e.lvRaw = v
		}

		if e.id == 0 {
			continue
		}

		if e.parentID == e.id {
			e.parentID = 0
		}

		entries = append(entries, e)
		idToEntry[e.id] = &entries[len(entries)-1]
	}

	// Find root (parentID == 0).
	for i := range entries {
		if entries[i].parentID == 0 {
			rootID = entries[i].id
			break
		}
	}

	// Find Forms under root.
	for i := range entries {
		if entries[i].parentID == rootID && entries[i].name == "Forms" {
			formsID = entries[i].id
			break
		}
	}

	if formsID == 0 {
		return nil, errors.New("image: Forms folder not found in MSysAccessStorage")
	}

	tree := &formStorageTree{
		children: make(map[string][]formNode),
	}

	// Collect form folders (direct children of Forms) and their children.
	formFolders := map[int32]string{} // ID -> folder name

	for i := range entries {
		if entries[i].parentID == formsID {
			name := entries[i].name
			if name == "\x03DirData" || name == "\x03DirDataCopy" {
				data, err := resolveIfNeeded(db, entries[i].lvRaw)
				if err == nil && len(data) > 0 {
					tree.dirData = data
				}

				continue
			}

			if name == "PropData" {
				continue
			}

			formFolders[entries[i].id] = name
		}
	}

	// Collect children of each form folder.
	for i := range entries {
		folderName, ok := formFolders[entries[i].parentID]
		if !ok {
			continue
		}

		node := formNode{name: entries[i].name}
		if len(entries[i].lvRaw) > 0 {
			data, err := resolveIfNeeded(db, entries[i].lvRaw)
			if err == nil {
				node.data = data
			}
		}

		tree.children[folderName] = append(tree.children[folderName], node)
	}

	return tree, nil
}

func resolveIfNeeded(db *Database, lvRaw []byte) ([]byte, error) {
	if len(lvRaw) == 0 {
		return nil, nil
	}

	return db.ResolveMemo(lvRaw)
}

func findTable(db *Database, name string) (*TableDef, error) {
	td, err := db.FindTable(name)
	if err == nil {
		return td, nil
	}

	entries, err := db.Catalog()
	if err != nil {
		return nil, fmt.Errorf("locate %s: %w", name, err)
	}

	for _, e := range entries {
		if e.Type == ObjTypeLocalTable && strings.EqualFold(e.Name, name) {
			return db.ReadTableDef(int64(e.ID))
		}
	}

	return nil, fmt.Errorf("table %q not found", name)
}

// parseFormDirData parses the \x03DirData stream to map numeric folder names to form names.
//
// Observed binary format:
//
//	4 bytes header (zeros)
//	then repeated entries:
//	  byte 0x04 (entry marker)
//	  byte entryLen (total bytes for name + 4-byte folder index)
//	  entryLen-4 bytes of UTF-16LE name
//	  uint32 LE folder index
func parseFormDirData(tree *formStorageTree) map[string]string {
	result := map[string]string{}
	if len(tree.dirData) < 6 {
		return result
	}

	d := tree.dirData
	i := 4 // skip 4-byte header

	for i+2 < len(d) {
		if d[i] != 0x04 {
			i++
			continue
		}

		i++ // skip 0x04 marker

		entryLen := int(d[i])
		i++

		if entryLen < 6 || i+entryLen > len(d) {
			continue
		}

		entry := d[i : i+entryLen]
		i += entryLen

		// Last 4 bytes are the folder index (LE uint32).
		nameBytes := entry[:len(entry)-4]
		folderIndex := binary.LittleEndian.Uint32(entry[len(entry)-4:])

		name := decodeUTF16LE(nameBytes)

		result[strconv.FormatUint(uint64(folderIndex), 10)] = name
	}

	return result
}

func decodeUTF16LE(b []byte) string {
	if len(b) < 2 {
		return ""
	}

	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}

	return string(utf16.Decode(u16))
}

// scanBlobForImages scans a form blob for embedded images.
func scanBlobForImages(data []byte, formName string) []EmbeddedImage {
	var images []EmbeddedImage
	seen := map[int]bool{} // deduplicate by offset

	// Scan for JPEG images.
	for off := 0; off < len(data)-3; {
		idx := bytes.Index(data[off:], sigJPEG)
		if idx < 0 {
			break
		}

		pos := off + idx
		if seen[pos] {
			off = pos + 3
			continue
		}

		// Try to get the image size from the Access record header preceding
		// the JPEG data. The header ends with a 4-byte LE size right before
		// the JPEG signature.
		size := jpegSizeFromRecordHeader(data, pos)
		if size <= 0 {
			// Fallback: find the matching FFD9 end marker, accounting for
			// nested JPEGs (e.g. Photoshop thumbnails inside APP13 segments).
			size = jpegSizeByMarkerPairing(data, pos)
		}

		if size <= 0 {
			off = pos + 3
			continue
		}

		imgData := make([]byte, size)
		copy(imgData, data[pos:pos+size])

		img := EmbeddedImage{
			FormName: formName,
			FileName: findFilename(data, pos),
			Format:   "jpeg",
			Data:     imgData,
		}
		images = append(images, img)
		seen[pos] = true
		off = pos + size
	}

	// Scan for PNG images.
	for off := 0; off < len(data)-8; {
		idx := bytes.Index(data[off:], sigPNG)
		if idx < 0 {
			break
		}

		pos := off + idx
		if seen[pos] {
			off = pos + 8
			continue
		}

		// PNG ends with IEND chunk.
		iend := []byte{0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}

		endIdx := bytes.Index(data[pos:], iend)
		if endIdx < 0 {
			off = pos + 8
			continue
		}

		size := endIdx + len(iend)
		imgData := make([]byte, size)
		copy(imgData, data[pos:pos+size])

		img := EmbeddedImage{
			FormName: formName,
			FileName: findFilename(data, pos),
			Format:   "png",
			Data:     imgData,
		}
		images = append(images, img)
		seen[pos] = true
		off = pos + size
	}

	// Scan for GIF images.
	for _, sig := range [][]byte{sigGIF87a, sigGIF89a} {
		for off := 0; off < len(data)-6; {
			idx := bytes.Index(data[off:], sig)
			if idx < 0 {
				break
			}

			pos := off + idx
			if seen[pos] {
				off = pos + 6
				continue
			}

			// GIF ends with 0x3B trailer.
			endIdx := bytes.Index(data[pos+6:], []byte{0x3B})
			if endIdx < 0 {
				off = pos + 6
				continue
			}

			size := 6 + endIdx + 1
			imgData := make([]byte, size)
			copy(imgData, data[pos:pos+size])

			img := EmbeddedImage{
				FormName: formName,
				FileName: findFilename(data, pos),
				Format:   "gif",
				Data:     imgData,
			}
			images = append(images, img)
			seen[pos] = true
			off = pos + size
		}
	}

	// Scan for BMP images.
	for off := 0; off < len(data)-14; {
		idx := bytes.Index(data[off:], sigBMP)
		if idx < 0 {
			break
		}

		pos := off + idx
		if seen[pos] {
			off = pos + 2
			continue
		}

		// BMP header has file size at offset 2 (4-byte LE).
		if pos+6 > len(data) {
			break
		}

		bmpSize := int(binary.LittleEndian.Uint32(data[pos+2:]))

		// Sanity check: BMP size should be reasonable and fit in the blob.
		if bmpSize < 14 || bmpSize > len(data)-pos || bmpSize > 50*1024*1024 {
			off = pos + 2
			continue
		}

		imgData := make([]byte, bmpSize)
		copy(imgData, data[pos:pos+bmpSize])

		img := EmbeddedImage{
			FormName: formName,
			FileName: findFilename(data, pos),
			Format:   "bmp",
			Data:     imgData,
		}
		images = append(images, img)
		seen[pos] = true
		off = pos + bmpSize
	}

	return images
}

// findFilename searches the blob data preceding an image for a UTF-16LE filename.
// Access form blobs store the original filename before image data.
func findFilename(data []byte, imageOffset int) string {
	// Search up to 512 bytes before the image for a filename.
	searchStart := max(imageOffset-512, 0)
	region := data[searchStart:imageOffset]

	// Look for common image file extensions in UTF-16LE.
	extensions := []string{".jpg", ".jpeg", ".png", ".bmp", ".gif"}
	for _, ext := range extensions {
		extBytes := encodeUTF16LE(ext)

		idx := bytes.LastIndex(region, extBytes)
		if idx < 0 {
			continue
		}

		// Walk backwards from the extension to find the start of the filename.
		nameEnd := idx + len(extBytes)

		nameStart := idx
		for nameStart >= 2 {
			ch := uint16(region[nameStart-2]) | uint16(region[nameStart-1])<<8
			if ch == 0 || ch < 0x20 {
				break
			}

			nameStart -= 2
		}

		if nameStart < nameEnd {
			name := decodeUTF16LE(region[nameStart:nameEnd])
			if len(name) > 0 {
				return name
			}
		}
	}

	return ""
}

// jpegSizeFromRecordHeader reads the image size from the Access form blob
// record header that precedes a JPEG. The header pattern is:
//
//	... 3e 02 0b 00 0xFF [size LE32] [JPEG start marker...]
//
// Returns 0 if no valid header is found.
func jpegSizeFromRecordHeader(data []byte, jpegOffset int) int {
	// The 4-byte size field is immediately before the JPEG signature,
	// preceded by the 10-byte sigImageRecordTail pattern.
	headerEnd := jpegOffset
	if headerEnd < 14 {
		return 0
	}

	sizeBytes := data[headerEnd-4 : headerEnd]
	tailBytes := data[headerEnd-14 : headerEnd-4]

	if !bytes.Equal(tailBytes, sigImageRecordTail) {
		return 0
	}

	size := int(binary.LittleEndian.Uint32(sizeBytes))
	if size <= 0 || jpegOffset+size > len(data) {
		return 0
	}

	return size
}

// jpegSizeByMarkerPairing determines the JPEG size by pairing nested
// FFD8/FFD9 markers. JPEGs with embedded thumbnails (e.g. Photoshop APP13)
// contain inner FFD8...FFD9 pairs that must not be mistaken for the outer end.
func jpegSizeByMarkerPairing(data []byte, jpegOffset int) int {
	depth := 1 // we already matched the outer FFD8
	pos := jpegOffset + 2

	for pos < len(data)-1 {
		// Scan for the next FFD8 or FFD9.
		nextStart := -1
		nextEnd := -1

		si := bytes.Index(data[pos:], sigJPEG)
		if si >= 0 {
			nextStart = pos + si
		}

		ei := bytes.Index(data[pos:], sigJPEGEnd)
		if ei >= 0 {
			nextEnd = pos + ei
		}

		if nextEnd < 0 {
			// No more FFD9 markers — can't determine size.
			return 0
		}

		if nextStart >= 0 && nextStart < nextEnd {
			// Found a nested FFD8 before the next FFD9.
			depth++
			pos = nextStart + 3

			continue
		}

		// Found an FFD9.
		depth--
		if depth == 0 {
			return nextEnd + 2 - jpegOffset
		}

		pos = nextEnd + 2
	}

	return 0
}

func encodeUTF16LE(s string) []byte {
	runes := []rune(s)
	u16 := utf16.Encode(runes)

	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}

	return b
}
