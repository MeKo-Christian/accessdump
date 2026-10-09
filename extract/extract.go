// Package extract provides a programmatic API for extracting VBA modules
// from Microsoft Access database files (.mdb / .accdb).
//
// Example usage:
//
//	modules, err := extract.Extract("MyDatabase.mdb", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, m := range modules {
//	    fmt.Printf("%s (%s):\n%s\n", m.Name, m.Type, m.Text)
//	}
package extract

import (
	"fmt"
	"log/slog"

	"github.com/MeKo-Christian/accessdump/internal/mdb"
	"github.com/MeKo-Christian/accessdump/internal/vba"
)

// ModuleType identifies the kind of VBA module.
type ModuleType string

const (
	ModuleTypeStandard ModuleType = "standard"
	ModuleTypeClass    ModuleType = "class"
	ModuleTypeDocument ModuleType = "document"
)

// Module holds the extracted source text and metadata for a single VBA module.
type Module struct {
	// Name is the module name as declared in the VBA project.
	Name string
	// Type is the module kind (standard, class, or document).
	Type ModuleType
	// Text is the full VBA source text.
	Text string
	// Partial is true when decompression fell back to a recovery strategy,
	// meaning the source may be truncated or contain artefacts.
	Partial bool
	// Warnings contains non-fatal issues encountered during extraction.
	Warnings []string
}

// Extract opens an Access database file and returns all VBA modules found in it.
// Returns nil, nil if the file contains no VBA project.
//
// Pass a *slog.Logger to receive debug messages (e.g. fallback strategy used).
// Pass nil to use slog.Default().
func Extract(path string, log *slog.Logger) ([]Module, error) {
	if log == nil {
		log = slog.Default()
	}

	db, err := mdb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer db.Close()

	st, stErr := vba.LoadStorageTree(db)

	var extracted []vba.ExtractedModule
	var extractErr error

	if stErr == nil {
		extracted, extractErr = vba.ExtractAllModules(st, log)
		if extractErr == nil && len(extracted) > 0 {
			return toModules(extracted), nil
		}
	} else {
		extractErr = stErr
	}

	// Fallback: scan raw LVAL chains for orphaned module streams.
	// This handles databases where MSysAccessStorage is missing entirely or
	// where the VBA structure was stripped but page data remains on disk.
	scanned, scanErr := vba.ScanOrphanedLvalModules(db)
	if scanErr == nil && len(scanned) > 0 {
		log.Debug("vba: standard extraction failed; recovered modules via raw LVAL scan",
			"err", extractErr, "count", len(scanned))

		return toModules(scanned), nil
	}

	// Both paths found nothing. If the only failure was "no VBA structure"
	// treat it as a database with no VBA rather than a hard error.
	if extractErr != nil && scanErr == nil {
		log.Debug("vba: no VBA found", "path", path, "err", extractErr)
		return nil, nil
	}

	if extractErr != nil {
		return nil, fmt.Errorf("extract modules %q: %w", path, extractErr)
	}

	return nil, nil
}

func toModules(in []vba.ExtractedModule) []Module {
	out := make([]Module, len(in))
	for i, m := range in {
		out[i] = Module{
			Name:     m.Name,
			Type:     ModuleType(m.Type),
			Text:     m.Text,
			Partial:  m.Partial,
			Warnings: m.Warnings,
		}
	}

	return out
}
