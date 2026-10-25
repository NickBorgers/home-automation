package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeFileDetectsLevelReaderRead(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "charging.go")
	source := `package tesla

func (m *Manager) energyLevel() string {
	level, _ := m.levels.GetString("currentEnergyLevel")
	return level
}
`
	if err := os.WriteFile(filename, []byte(source), 0600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	analysis, err := analyzeFile(filename, "tesla")
	if err != nil {
		t.Fatalf("analyze source: %v", err)
	}
	if !contains(analysis.Reads, "currentEnergyLevel") {
		t.Fatalf("expected currentEnergyLevel read, got %v", analysis.Reads)
	}
}
