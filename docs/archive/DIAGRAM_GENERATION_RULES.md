# Automated Diagram Generation Rules

This document defines rules and patterns for automatically generating Mermaid diagrams from the Golang codebase.

## Overview

While manual diagrams (see [VISUAL_ARCHITECTURE.md](./VISUAL_ARCHITECTURE.md)) are easier to maintain, automated generation ensures diagrams stay synchronized with code changes.

## Current Status

**Status:** NOT IMPLEMENTED - This document defines the rules for future automation

**Recommendation:** Start with manual diagrams, then automate as patterns stabilize.

---

## Option 2A: Code Annotation-Based Generation

### Concept
Add structured comments to Go code that diagram generators can parse.

### Example: Plugin Registration

```go
// @diagram:plugin
// @inputs: isAnyoneHome, isAnyoneAsleep, dayPhase
// @outputs: musicPlaybackType, currentlyPlayingMusicUri
// @triggers: dayPhase, isAnyoneAsleep, isAnyoneHome
// @description: Selects appropriate music mode based on time of day and occupancy
type Manager struct {
    // ...
}
```

### Example: State Variable Dependency

```go
// @diagram:state-variable
// @type: boolean
// @computed-from: isNickHome, isCarolineHome, isToriHere
// @used-by: music-plugin, lighting-plugin, security-plugin
var isAnyoneHome = StateVariable{
    Key: "isAnyoneHome",
    // ...
}
```

### Example: Decision Logic

```go
// @diagram:decision-tree
func (m *Manager) selectAppropriateMusicMode() {
    // @node: Check if anyone is home
    isAnyoneHome, _ := m.stateManager.GetBool("isAnyoneHome")

    // @branch: no-one-home -> stop-music
    if !isAnyoneHome {
        m.setMusicPlaybackType("")
        return
    }

    // @node: Check if anyone is asleep
    isAnyoneAsleep, _ := m.stateManager.GetBool("isAnyoneAsleep")

    // @branch: someone-asleep -> sleep-mode
    if isAnyoneAsleep {
        m.setMusicPlaybackType("sleep")
        return
    }

    // @node: Determine mode from day phase
    dayPhase, _ := m.stateManager.GetString("dayPhase")
    musicMode := m.determineMusicModeFromDayPhase(dayPhase, ...)

    // @action: Set music playback type
    m.setMusicPlaybackType(musicMode)
}
```

### Generator Script (Pseudocode)

```python
#!/usr/bin/env python3
"""
Generate Mermaid diagrams from annotated Go code.

Usage:
    python3 generate_diagrams.py --input homeautomation-go/ --output docs/diagrams/
"""

import re
import os
from dataclasses import dataclass

@dataclass
class PluginMetadata:
    name: str
    inputs: list[str]
    outputs: list[str]
    triggers: list[str]
    description: str

def extract_plugin_metadata(go_file: str) -> PluginMetadata:
    """Extract @diagram:plugin annotations from Go file."""
    with open(go_file) as f:
        content = f.read()

    # Parse annotations
    inputs = re.findall(r'@inputs:\s*(.*)', content)
    outputs = re.findall(r'@outputs:\s*(.*)', content)
    triggers = re.findall(r'@triggers:\s*(.*)', content)
    description = re.findall(r'@description:\s*(.*)', content)

    # ...parse and return metadata

def generate_plugin_architecture_diagram(plugins: list[PluginMetadata]) -> str:
    """Generate Mermaid diagram showing plugin interactions."""
    mermaid = ["```mermaid", "graph TB"]

    # Add state variables as nodes
    all_inputs = set()
    for plugin in plugins:
        all_inputs.update(plugin.inputs)

    for input_var in all_inputs:
        mermaid.append(f'    {input_var}["{input_var}"]')

    # Add plugins as nodes
    for plugin in plugins:
        mermaid.append(f'    {plugin.name}["{plugin.name}<br/>{plugin.description}"]')

    # Add edges (input variables -> plugins)
    for plugin in plugins:
        for input_var in plugin.inputs:
            mermaid.append(f'    {input_var} --> {plugin.name}')

    # Add edges (plugins -> output variables)
    for plugin in plugins:
        for output_var in plugin.outputs:
            mermaid.append(f'    {plugin.name} --> {output_var}["{output_var}"]')

    mermaid.append("```")
    return "\n".join(mermaid)

# Main execution
if __name__ == "__main__":
    plugins = []
    for root, dirs, files in os.walk("homeautomation-go/internal/plugins/"):
        for file in files:
            if file.endswith("manager.go"):
                metadata = extract_plugin_metadata(os.path.join(root, file))
                if metadata:
                    plugins.append(metadata)

    diagram = generate_plugin_architecture_diagram(plugins)
    print(diagram)
```

---

## Option 2B: AST-Based Generation

### Concept
Parse Go code using `go/ast` to extract structure without annotations.

### What Can Be Auto-Generated

#### 1. **Plugin Dependency Graph**

Extract from:
- Plugin struct fields (dependencies)
- `Subscribe()` calls in `Start()` method
- `GetBool/GetString/GetNumber/SetBool/SetString/SetNumber` calls

```go
// Automatically detected:
sub, err := m.stateManager.Subscribe("dayPhase", m.handleStateChange)
// → Plugin subscribes to "dayPhase"

isAnyoneHome, _ := m.stateManager.GetBool("isAnyoneHome")
// → Plugin reads "isAnyoneHome"

m.stateManager.SetString("musicPlaybackType", mode)
// → Plugin writes "musicPlaybackType"
```

#### 2. **Call Graph / Sequence Diagrams**

Extract from:
- Function call chains
- Method receivers

```go
func (m *Manager) Start() {
    m.selectAppropriateMusicMode()  // → generates call arrow
}

func (m *Manager) selectAppropriateMusicMode() {
    m.setMusicPlaybackType(mode)  // → generates nested call arrow
}
```

#### 3. **State Variable Usage Matrix**

Generate table showing which plugins use which variables.

| Variable | Music | Lighting | Energy | TV | Sleep | Security | Load Shedding |
|----------|-------|----------|--------|----|----|----------|---------------|
| isAnyoneHome | R | R | - | - | - | R | - |
| isAnyoneAsleep | R | R | - | - | R | - | - |
| dayPhase | R | R | - | - | - | - | - |
| musicPlaybackType | W | - | - | - | - | - | - |

**Legend:** R = Read, W = Write, - = Not used

### Generator Tool (Go-based)

```go
package main

import (
    "go/ast"
    "go/parser"
    "go/token"
    "fmt"
)

type PluginAnalysis struct {
    Name string
    ReadsVariables []string
    WritesVariables []string
    Subscribes []string
}

func analyzePlugin(filename string) (*PluginAnalysis, error) {
    fset := token.NewFileSet()
    node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
    if err != nil {
        return nil, err
    }

    analysis := &PluginAnalysis{
        Name: extractPluginName(filename),
    }

    // Walk the AST
    ast.Inspect(node, func(n ast.Node) bool {
        // Look for method calls like m.stateManager.GetBool("isAnyoneHome")
        if call, ok := n.(*ast.CallExpr); ok {
            if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
                methodName := sel.Sel.Name

                switch methodName {
                case "GetBool", "GetString", "GetNumber", "GetJSON":
                    if len(call.Args) > 0 {
                        if lit, ok := call.Args[0].(*ast.BasicLit); ok {
                            varName := strings.Trim(lit.Value, `"`)
                            analysis.ReadsVariables = append(analysis.ReadsVariables, varName)
                        }
                    }

                case "SetBool", "SetString", "SetNumber", "SetJSON":
                    if len(call.Args) > 0 {
                        if lit, ok := call.Args[0].(*ast.BasicLit); ok {
                            varName := strings.Trim(lit.Value, `"`)
                            analysis.WritesVariables = append(analysis.WritesVariables, varName)
                        }
                    }

                case "Subscribe":
                    if len(call.Args) > 0 {
                        if lit, ok := call.Args[0].(*ast.BasicLit); ok {
                            varName := strings.Trim(lit.Value, `"`)
                            analysis.Subscribes = append(analysis.Subscribes, varName)
                        }
                    }
                }
            }
        }
        return true
    })

    return analysis, nil
}

func generateDependencyDiagram(analyses []*PluginAnalysis) string {
    // Generate Mermaid diagram from analyses
    var lines []string
    lines = append(lines, "```mermaid")
    lines = append(lines, "graph TB")

    // ... generate nodes and edges

    lines = append(lines, "```")
    return strings.Join(lines, "\n")
}
```

---

## Option 2C: Runtime Instrumentation

### Concept
Monitor the running application and generate diagrams from actual behavior.

### Implementation

```go
// Add tracing to state manager
func (m *Manager) GetBool(key string) (bool, error) {
    // Record read operation
    if m.tracer != nil {
        m.tracer.RecordRead(getCurrentPlugin(), key)
    }

    // ... existing implementation
}

func (m *Manager) SetBool(key string, value bool) error {
    // Record write operation
    if m.tracer != nil {
        m.tracer.RecordWrite(getCurrentPlugin(), key, value)
    }

    // ... existing implementation
}
```

**Output:** JSON trace file
```json
{
  "operations": [
    {"timestamp": "2025-11-16T10:30:00Z", "plugin": "music", "operation": "read", "variable": "isAnyoneHome"},
    {"timestamp": "2025-11-16T10:30:01Z", "plugin": "music", "operation": "write", "variable": "musicPlaybackType", "value": "day"}
  ]
}
```

**Converter:** Trace JSON → Mermaid sequence diagram

---

## Recommendation: Hybrid Approach

### Phase 1: Manual Diagrams (CURRENT)
✅ Created in [VISUAL_ARCHITECTURE.md](./VISUAL_ARCHITECTURE.md)
- Fast to create
- Easy to maintain
- Full control over presentation
- **Best for architectural/high-level diagrams**

### Phase 2: AST-Based Generation (FUTURE)
⏳ Implement when:
- Code structure stabilizes
- Need to track dependencies automatically
- **Best for dependency graphs and variable usage matrices**

### Phase 3: Runtime Instrumentation (OPTIONAL)
⏳ Implement if:
- Need to debug complex interactions
- Want to visualize actual runtime behavior
- **Best for sequence diagrams and performance analysis**

---

## Tools & Libraries

### For Manual Diagrams
- **Mermaid Live Editor:** https://mermaid.live/
- **VS Code Extension:** "Markdown Preview Mermaid Support"
- **GitHub:** Native Mermaid rendering in markdown

### For Automated Generation

#### Go AST Parsing
- `go/ast` - Go standard library
- `go/parser` - Go standard library
- `golang.org/x/tools/go/packages` - Package loading

#### Diagram Generation
- `github.com/dominikbraun/graph` - Graph data structure for Go
- Custom Mermaid string builders

#### Python Alternative
- `ast` module for Go parsing (limited)
- `pycparser` for more complex parsing
- `diagrams` library for diagram generation

---

## Maintenance Strategy

### When to Update Diagrams

**Trigger Events:**
1. New plugin added
2. Plugin dependencies change (new Subscribe calls)
3. State variable added/removed
4. Major architectural refactor

**Update Process:**
1. Identify affected diagrams in [VISUAL_ARCHITECTURE.md](./VISUAL_ARCHITECTURE.md)
2. Update diagram source
3. Verify rendering in GitHub preview
4. Update "Last Updated" timestamp
5. Include in PR with code changes

### Diagram Ownership

| Diagram Type | Owner | Update Frequency |
|--------------|-------|------------------|
| System Architecture | Lead Developer | Major releases |
| Plugin Architecture | Plugin Authors | Per plugin change |
| State Flow | State Team | State variable changes |
| Individual Plugin Logic | Plugin Authors | Plugin logic changes |

---

## Future Enhancements

### 1. **CI/CD Integration**
```yaml
# .github/workflows/diagram-check.yml
name: Diagram Freshness Check

on: [pull_request]

jobs:
  check-diagrams:
    runs-on: ubuntu-latest
    steps:
      - name: Check if diagrams are up-to-date
        run: |
          # Run AST-based generator
          python3 scripts/generate_diagrams.py --check

          # Compare with committed diagrams
          # Fail if diagrams are stale
```

### 2. **Interactive Diagrams**
- Generate HTML with clickable nodes linking to code
- Use tools like `d3.js` or `cytoscape.js`

### 3. **Diff Visualization**
- Show what changed between commits
- Highlight new/removed dependencies

---

**Last Updated:** 2025-11-16
**Status:** Planning Document - Manual diagrams created, automation not yet implemented
