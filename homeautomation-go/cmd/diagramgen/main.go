// Package main provides an AST-based tool to analyze plugin source files
// and generate Mermaid diagrams showing state variable dependencies.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginAnalysis holds the extracted state variable usage for a plugin
type PluginAnalysis struct {
	Name       string
	Subscribes []string // Variables subscribed to
	Reads      []string // Variables read (GetBool/GetString/GetNumber)
	Writes     []string // Variables written (SetBool/SetString/SetNumber)
}

func main() {
	// Find the project root (where go.mod is)
	projectRoot, err := findProjectRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding project root: %v\n", err)
		os.Exit(1)
	}

	pluginsDir := filepath.Join(projectRoot, "internal", "plugins")
	outputDir := filepath.Join(projectRoot, "..", "docs", "generated")

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	// Find all plugin manager.go files
	plugins, err := analyzePlugins(pluginsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing plugins: %v\n", err)
		os.Exit(1)
	}

	// Generate Mermaid diagram
	diagram := generateMermaidDiagram(plugins)
	diagramPath := filepath.Join(outputDir, "plugin-dependencies.md")
	if err := os.WriteFile(diagramPath, []byte(diagram), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing diagram: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated: %s\n", diagramPath)

	// Generate state variable matrix
	matrix := generateStateMatrix(plugins)
	matrixPath := filepath.Join(outputDir, "state-variable-matrix.md")
	if err := os.WriteFile(matrixPath, []byte(matrix), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing matrix: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated: %s\n", matrixPath)

	fmt.Printf("\nAnalyzed %d plugins\n", len(plugins))
}

// findProjectRoot walks up to find the directory containing go.mod
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

// analyzePlugins finds and analyzes all plugin manager.go files
func analyzePlugins(pluginsDir string) ([]PluginAnalysis, error) {
	var plugins []PluginAnalysis

	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil, fmt.Errorf("reading plugins directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginName := entry.Name()
		managerPath := filepath.Join(pluginsDir, pluginName, "manager.go")

		if _, err := os.Stat(managerPath); os.IsNotExist(err) {
			continue
		}

		analysis, err := analyzeFile(managerPath, pluginName)
		if err != nil {
			return nil, fmt.Errorf("analyzing %s: %w", pluginName, err)
		}

		plugins = append(plugins, analysis)
	}

	// Sort plugins by name for consistent output
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})

	return plugins, nil
}

// analyzeFile parses a Go file and extracts state variable usage
func analyzeFile(filename, pluginName string) (PluginAnalysis, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return PluginAnalysis{}, err
	}

	analysis := PluginAnalysis{
		Name: pluginName,
	}

	// Track unique variables
	subscribes := make(map[string]bool)
	reads := make(map[string]bool)
	writes := make(map[string]bool)

	// Walk the AST
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check for method calls like m.stateManager.GetBool("var")
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		methodName := sel.Sel.Name

		// Check if this is a state manager call
		if !isStateManagerCall(sel) {
			return true
		}

		// Extract the first string argument (variable name)
		if len(call.Args) == 0 {
			return true
		}

		varName := extractStringLiteral(call.Args[0])
		if varName == "" {
			return true
		}

		switch methodName {
		case "Subscribe", "SubscribeToState":
			subscribes[varName] = true
		case "GetBool", "GetString", "GetNumber", "GetJSON":
			reads[varName] = true
		case "SetBool", "SetString", "SetNumber", "SetJSON":
			writes[varName] = true
		}

		return true
	})

	// Convert maps to sorted slices
	analysis.Subscribes = mapKeysToSortedSlice(subscribes)
	analysis.Reads = mapKeysToSortedSlice(reads)
	analysis.Writes = mapKeysToSortedSlice(writes)

	return analysis, nil
}

// isStateManagerCall checks if the selector expression is a stateManager method call
func isStateManagerCall(sel *ast.SelectorExpr) bool {
	// Look for patterns like:
	// - m.stateManager.Method()
	// - m.subHelper.SubscribeToState() -> treat as Subscribe

	if innerSel, ok := sel.X.(*ast.SelectorExpr); ok {
		fieldName := innerSel.Sel.Name
		return fieldName == "stateManager" || fieldName == "subHelper"
	}
	return false
}

// extractStringLiteral extracts a string value from a basic literal
func extractStringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	// Remove quotes
	return strings.Trim(lit.Value, `"`)
}

// mapKeysToSortedSlice converts a map's keys to a sorted slice
func mapKeysToSortedSlice(m map[string]bool) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// generateMermaidDiagram creates a Mermaid graph showing plugin dependencies
func generateMermaidDiagram(plugins []PluginAnalysis) string {
	var sb strings.Builder

	sb.WriteString("# Plugin State Variable Dependencies\n\n")
	sb.WriteString("This file is auto-generated by `make generate-diagrams`. Do not edit manually.\n\n")
	sb.WriteString("```mermaid\n")
	sb.WriteString("graph LR\n")

	// Collect all unique state variables
	allVars := make(map[string]bool)
	for _, p := range plugins {
		for _, v := range p.Subscribes {
			allVars[v] = true
		}
		for _, v := range p.Reads {
			allVars[v] = true
		}
		for _, v := range p.Writes {
			allVars[v] = true
		}
	}

	// Sort variables for consistent output
	sortedVars := mapKeysToSortedSlice(allVars)

	// Define state variable nodes
	sb.WriteString("    subgraph StateVariables[\"State Variables\"]\n")
	for _, v := range sortedVars {
		sb.WriteString(fmt.Sprintf("        %s[\"%s\"]\n", sanitizeID(v), v))
	}
	sb.WriteString("    end\n\n")

	// Define plugin nodes
	sb.WriteString("    subgraph Plugins[\"Plugins\"]\n")
	for _, p := range plugins {
		sb.WriteString(fmt.Sprintf("        %s[\"%s\"]\n", sanitizeID(p.Name), p.Name))
	}
	sb.WriteString("    end\n\n")

	// Add edges: subscriptions (dashed)
	sb.WriteString("    %% Subscriptions (plugin listens to variable)\n")
	for _, p := range plugins {
		for _, v := range p.Subscribes {
			sb.WriteString(fmt.Sprintf("    %s -.-> %s\n", sanitizeID(v), sanitizeID(p.Name)))
		}
	}

	// Add edges: writes (solid, plugin -> variable)
	sb.WriteString("\n    %% Writes (plugin sets variable)\n")
	for _, p := range plugins {
		for _, v := range p.Writes {
			sb.WriteString(fmt.Sprintf("    %s --> %s\n", sanitizeID(p.Name), sanitizeID(v)))
		}
	}

	sb.WriteString("```\n\n")

	// Add legend
	sb.WriteString("## Legend\n\n")
	sb.WriteString("- **Dashed arrows** (`-.->`) = Plugin subscribes to variable changes\n")
	sb.WriteString("- **Solid arrows** (`-->`) = Plugin writes to variable\n")
	sb.WriteString("- Reads are not shown to reduce visual clutter\n")

	return sb.String()
}

// generateStateMatrix creates a markdown table showing variable usage by plugin
func generateStateMatrix(plugins []PluginAnalysis) string {
	var sb strings.Builder

	sb.WriteString("# State Variable Usage Matrix\n\n")
	sb.WriteString("This file is auto-generated by `make generate-diagrams`. Do not edit manually.\n\n")

	// Collect all unique state variables
	allVars := make(map[string]bool)
	for _, p := range plugins {
		for _, v := range p.Subscribes {
			allVars[v] = true
		}
		for _, v := range p.Reads {
			allVars[v] = true
		}
		for _, v := range p.Writes {
			allVars[v] = true
		}
	}

	sortedVars := mapKeysToSortedSlice(allVars)

	// Build header
	sb.WriteString("| Variable |")
	for _, p := range plugins {
		sb.WriteString(fmt.Sprintf(" %s |", p.Name))
	}
	sb.WriteString("\n")

	// Build separator
	sb.WriteString("|----------|")
	for range plugins {
		sb.WriteString("--------|")
	}
	sb.WriteString("\n")

	// Build rows
	for _, v := range sortedVars {
		sb.WriteString(fmt.Sprintf("| %s |", v))
		for _, p := range plugins {
			usage := getUsageString(v, p)
			sb.WriteString(fmt.Sprintf(" %s |", usage))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Legend\n\n")
	sb.WriteString("- **R** = Reads variable (GetBool/GetString/GetNumber)\n")
	sb.WriteString("- **W** = Writes variable (SetBool/SetString/SetNumber)\n")
	sb.WriteString("- **S** = Subscribes to variable changes\n")
	sb.WriteString("- **-** = Not used by this plugin\n")

	return sb.String()
}

// getUsageString returns R/W/S indicators for a variable/plugin combination
func getUsageString(variable string, plugin PluginAnalysis) string {
	var parts []string

	if contains(plugin.Reads, variable) {
		parts = append(parts, "R")
	}
	if contains(plugin.Writes, variable) {
		parts = append(parts, "W")
	}
	if contains(plugin.Subscribes, variable) {
		parts = append(parts, "S")
	}

	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// contains checks if a slice contains a string
func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// sanitizeID converts a variable name to a valid Mermaid node ID
func sanitizeID(s string) string {
	// Replace characters that might cause issues in Mermaid
	result := strings.ReplaceAll(s, "-", "_")
	result = strings.ReplaceAll(result, " ", "_")
	return result
}
