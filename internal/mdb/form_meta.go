package mdb

import (
	"encoding/binary"
	"strings"
)

// FormMeta holds UI metadata extracted heuristically from a form's Blob stream.
//
// The form Blob is a proprietary binary property-bag format. Rather than
// parsing it structurally, we scan for UTF-16LE string runs and classify them
// by content:
//   - Strings starting with "SELECT" or "TABLE" are RecordSource/RowSource SQL.
//   - "[Event Procedure]" and strings starting with "=" are event expressions.
//   - Other strings of 3+ printable characters are treated as control names.
type FormMeta struct {
	Name          string   // form name, from DirData
	RecordSource  string   // first SQL or table-name string found in blob
	EventHandlers []string // distinct event expressions (deduplicated)
	Controls      []string // distinct control/label names (deduplicated)
}

// ScanFormBlobs reads all form blobs from MSysAccessStorage and returns a
// FormMeta per form. Forms that cannot be read are silently skipped.
func ScanFormBlobs(db *Database) ([]FormMeta, error) {
	tree, err := loadFormStorageTree(db)
	if err != nil {
		return nil, err
	}

	formNames := parseFormDirData(tree)

	var result []FormMeta

	for folderName, children := range tree.children {
		formName := formNames[folderName]
		if formName == "" {
			formName = "Form_" + folderName
		}

		for _, child := range children {
			if child.name != "Blob" || len(child.data) == 0 {
				continue
			}

			strs := scanBlobStrings(child.data)
			meta := classifyBlobStrings(formName, strs)
			result = append(result, meta)
		}
	}

	return result, nil
}

// scanBlobStrings scans a form Blob for UTF-16LE string runs of 3 or more
// printable characters and returns them as Go strings.
//
// The Access form blob stores all string property values (control names,
// captions, event expressions, SQL) as UTF-16LE without a common framing
// header. Adjacent bytes that do not form printable BMP code-points terminate
// each run.
func scanBlobStrings(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	const minLen = 3 // minimum printable characters to keep

	var results []string
	i := 0

	for i+2 <= len(data) {
		start := i
		var u16 []uint16

		for i+2 <= len(data) {
			w := binary.LittleEndian.Uint16(data[i:])
			if isPrintableBMP(w) {
				u16 = append(u16, w)
				i += 2
			} else {
				break
			}
		}

		if len(u16) >= minLen {
			// The last code unit is typically a single-byte Access property-type
			// tag that forms a spurious UTF-16LE character (e.g. 0x00E3 → "ã").
			// Strip it if it is non-ASCII (>= 0x80) and the remainder is still
			// long enough.
			if u16[len(u16)-1] >= 0x80 && len(u16)-1 >= minLen {
				u16 = u16[:len(u16)-1]
			}

			results = append(results, decodeUTF16LE(u16ToBytes(u16)))
		}

		if i == start {
			i++
		}
	}

	return results
}

// classifyBlobStrings takes the raw strings extracted from a blob and returns
// a FormMeta with RecordSource, EventHandlers, and Controls filled in.
func classifyBlobStrings(formName string, strs []string) FormMeta {
	meta := FormMeta{Name: formName}

	seenEvents := map[string]bool{}
	seenControls := map[string]bool{}

	for _, s := range strs {
		upper := strings.ToUpper(s)

		switch {
		case strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "SELECT\t"):
			// SQL RecordSource or RowSource — keep the first one found.
			if meta.RecordSource == "" {
				meta.RecordSource = strings.TrimRight(s, " \t\r\n")
			}

		case s == "[Event Procedure]" || strings.HasPrefix(s, "="):
			// Event marker or expression-style event binding.
			if !seenEvents[s] {
				meta.EventHandlers = append(meta.EventHandlers, s)
				seenEvents[s] = true
			}

		default:
			// Treat as a control/label name — exclude obvious noise
			// (font names, section headers, etc.).
			if !isNoisyString(s) && !seenControls[s] {
				meta.Controls = append(meta.Controls, s)
				seenControls[s] = true
			}
		}
	}

	return meta
}

// isPrintableBMP returns true for UTF-16 code units in the printable BMP
// range that commonly appear in Access form property strings.
func isPrintableBMP(w uint16) bool {
	// Printable ASCII.
	if w >= 0x20 && w < 0x7F {
		return true
	}
	// Latin Extended and Latin-1 Supplement (covers German umlauts etc.)
	if w >= 0x00A0 && w <= 0x024F {
		return true
	}

	return false
}

// u16ToBytes converts a []uint16 to a little-endian byte slice.
func u16ToBytes(u16 []uint16) []byte {
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}

	return b
}

// isNoisyString returns true for strings that are clearly not control names
// (font names, section labels produced by Access itself, etc.).
func isNoisyString(s string) bool {
	noisy := []string{
		"Arial", "MS Sans Serif", "Times New Roman", "System",
		"Formularkopf", "Detailbereich", "Formularfuß",
		"Table/Query",
	}
	for _, n := range noisy {
		if s == n || strings.HasPrefix(s, n) {
			return true
		}
	}

	return false
}
