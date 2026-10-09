package mdb

import (
	"slices"
	"strings"
	"testing"
)

// --- Unit tests for scanBlobStrings ---

// TestScanBlobStrings_empty returns nothing for an empty blob.
func TestScanBlobStrings_empty(t *testing.T) {
	got := scanBlobStrings(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// TestScanBlobStrings_extractsUTF16LE verifies that a known UTF-16LE string is
// extracted from a blob that contains it surrounded by non-printable bytes.
func TestScanBlobStrings_extractsUTF16LE(t *testing.T) {
	// Encode "[Event Procedure]" as UTF-16LE.
	blob := encodeUTF16LE("[Event Procedure]")
	// Wrap it in some non-printable bytes.
	data := append([]byte{0x00, 0x03}, blob...)
	data = append(data, 0xAA, 0x00)

	got := scanBlobStrings(data)
	if len(got) == 0 {
		t.Fatal("expected at least one string, got none")
	}

	found := slices.Contains(got, "[Event Procedure]")

	if !found {
		t.Errorf("expected '[Event Procedure]' in %v", got)
	}
}

// TestScanBlobStrings_ignoresShortRuns verifies that runs shorter than 3
// characters are not returned.
func TestScanBlobStrings_ignoresShortRuns(t *testing.T) {
	blob := encodeUTF16LE("AB")

	got := scanBlobStrings(blob)
	for _, s := range got {
		if s == "AB" {
			t.Errorf("short string 'AB' should not be returned")
		}
	}
}

// --- Unit tests for classifyBlobStrings ---

// TestClassifyBlobStrings_eventProcedure verifies that "[Event Procedure]" is
// classified as an event marker.
func TestClassifyBlobStrings_eventProcedure(t *testing.T) {
	strs := []string{"MyControl", "[Event Procedure]", "AnotherControl"}
	meta := classifyBlobStrings("TestForm", strs)

	found := slices.Contains(meta.EventHandlers, "[Event Procedure]")

	if !found {
		t.Errorf("expected '[Event Procedure]' in EventHandlers, got %v", meta.EventHandlers)
	}
}

// TestClassifyBlobStrings_expressionEvent verifies that "=Func()" style strings
// are classified as event handlers.
func TestClassifyBlobStrings_expressionEvent(t *testing.T) {
	strs := []string{"=HandleButtonClick(1)"}
	meta := classifyBlobStrings("TestForm", strs)

	if len(meta.EventHandlers) == 0 {
		t.Fatal("expected at least one event handler")
	}

	if meta.EventHandlers[0] != "=HandleButtonClick(1)" {
		t.Errorf("expected '=HandleButtonClick(1)', got %q", meta.EventHandlers[0])
	}
}

// TestClassifyBlobStrings_recordSource verifies that a SELECT statement is
// classified as the RecordSource.
func TestClassifyBlobStrings_recordSource(t *testing.T) {
	sql := "SELECT OrderID, Total FROM Orders;"
	strs := []string{"MyForm", sql}
	meta := classifyBlobStrings("TestForm", strs)

	if meta.RecordSource != sql {
		t.Errorf("expected RecordSource=%q, got %q", sql, meta.RecordSource)
	}
}

// TestClassifyBlobStrings_formNamePreserved verifies that the form name passed
// in is preserved in the returned FormMeta.
func TestClassifyBlobStrings_formNamePreserved(t *testing.T) {
	meta := classifyBlobStrings("Startschirm", nil)
	if meta.Name != "Startschirm" {
		t.Errorf("expected name 'Startschirm', got %q", meta.Name)
	}
}

// TestClassifyBlobStrings_deduplicatesEventHandlers verifies that duplicate
// event handler strings appear only once.
func TestClassifyBlobStrings_deduplicatesEventHandlers(t *testing.T) {
	strs := []string{"[Event Procedure]", "[Event Procedure]", "[Event Procedure]"}
	meta := classifyBlobStrings("F", strs)

	if len(meta.EventHandlers) != 1 {
		t.Errorf("expected 1 deduplicated handler, got %d: %v", len(meta.EventHandlers), meta.EventHandlers)
	}
}

// --- Integration test against Start.mdb ---

// TestScanFormBlobs_startMDB verifies that ScanFormBlobs extracts FormMeta
// from the real Start.mdb fixture.
func TestScanFormBlobs_startMDB(t *testing.T) {
	db := startDB(t)

	forms, err := ScanFormBlobs(db)
	if err != nil {
		t.Fatalf("ScanFormBlobs: %v", err)
	}

	if len(forms) == 0 {
		t.Fatal("expected at least one form, got none")
	}

	byName := make(map[string]FormMeta)
	for _, f := range forms {
		byName[f.Name] = f
	}

	t.Logf("Forms found: %d", len(forms))

	for _, f := range forms {
		t.Logf("  %q: RecordSource=%q, EventHandlers=%d",
			f.Name, f.RecordSource, len(f.EventHandlers))
	}

	// Module_Übersicht has a SELECT statement as its row source.
	mod, ok := byName["Module_Übersicht"]
	if !ok {
		t.Error("expected form 'Module_Übersicht' in results")
	} else if !strings.HasPrefix(mod.RecordSource, "SELECT") {
		t.Errorf("Module_Übersicht: expected SELECT RecordSource, got %q", mod.RecordSource)
	}

	// Übersicht has =HandleButtonClick() expression events.
	ueb, ok := byName["Übersicht"]
	if !ok {
		t.Error("expected form 'Übersicht' in results")
	} else {
		found := false

		for _, e := range ueb.EventHandlers {
			if strings.HasPrefix(e, "=HandleButtonClick(") {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("Übersicht: expected =HandleButtonClick() event, got %v", ueb.EventHandlers)
		}
	}
}
