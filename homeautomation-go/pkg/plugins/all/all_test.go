package all

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllPluginsRegistered ensures every plugin with a register.go file
// is imported in all.go. This prevents accidentally forgetting to wire up
// new plugins (which happened with infrastructure and waterflow).
func TestAllPluginsRegistered(t *testing.T) {
	// Find the project root by looking for go.mod
	projectRoot := findProjectRoot(t)
	pluginsDir := filepath.Join(projectRoot, "internal", "plugins")
	allGoPath := filepath.Join(projectRoot, "pkg", "plugins", "all", "all.go")

	// Read all.go content
	allGoContent, err := os.ReadFile(allGoPath)
	if err != nil {
		t.Fatalf("Failed to read all.go: %v", err)
	}
	allGoStr := string(allGoContent)

	// Find all plugin directories with register.go
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatalf("Failed to read plugins directory: %v", err)
	}

	var missing []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginName := entry.Name()
		registerPath := filepath.Join(pluginsDir, pluginName, "register.go")

		// Check if this plugin has a register.go (meaning it should be auto-registered)
		if _, err := os.Stat(registerPath); os.IsNotExist(err) {
			// No register.go - this plugin is wired up differently (e.g., reset coordinator)
			continue
		}

		// Check if this plugin is imported in all.go
		expectedImport := `"homeautomation/internal/plugins/` + pluginName + `"`
		if !strings.Contains(allGoStr, expectedImport) {
			missing = append(missing, pluginName)
		}
	}

	if len(missing) > 0 {
		t.Errorf("The following plugins have register.go but are not imported in pkg/plugins/all/all.go:\n")
		for _, name := range missing {
			t.Errorf("  - %s (add: _ \"homeautomation/internal/plugins/%s\")", name, name)
		}
		t.Errorf("\nThis means these plugins will not run in production!")
	}
}

func findProjectRoot(t *testing.T) string {
	t.Helper()

	// Start from current working directory and walk up
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root (no go.mod found)")
		}
		dir = parent
	}
}
