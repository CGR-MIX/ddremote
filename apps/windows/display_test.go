package main

import (
	"encoding/json"
	"os"
	"testing"
	"unsafe"
)

func TestDisplayNativeLayout(t *testing.T) {
	for name, pair := range map[string][2]uintptr{"source": {unsafe.Sizeof(displaySource{}), 20}, "target": {unsafe.Sizeof(displayTarget{}), 48}, "path": {unsafe.Sizeof(displayPath{}), 72}, "mode": {unsafe.Sizeof(displayMode{}), 64}, "header": {unsafe.Sizeof(displayInfoHeader{}), 20}, "targetName": {unsafe.Sizeof(displayTargetName{}), 420}} {
		if pair[0] != pair[1] {
			t.Fatalf("%s native size=%d want=%d", name, pair[0], pair[1])
		}
	}
}
func TestDisplaySnapshotRejectsEmpty(t *testing.T) {
	if (DisplaySnapshot{}).validate() == nil {
		t.Fatal("empty snapshot accepted")
	}
}
func TestReadOnlyDisplayInventory(t *testing.T) {
	if os.Getenv("DD_TEST_DISPLAY_INVENTORY") == "" {
		t.Skip("opt-in real read-only display probe")
	}
	snap, err := captureDisplaySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err = snap.validate(); err != nil {
		t.Fatal(err)
	}
	displays, err := listDisplays()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(map[string]any{"displays": displays, "restoreSnapshotValidation": "passed (no apply)", "paths": len(snap.Paths), "modes": len(snap.Modes)}, "", "  ")
	if err = os.WriteFile(os.Getenv("DD_TEST_DISPLAY_INVENTORY"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
