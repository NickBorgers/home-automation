# Automation Flows

This directory contains visual documentation of the home automation system's behavior, primarily using Mermaid diagrams. These documents serve as the "visual representation of automations" similar to what Node-RED's flow editor provided.

## Purpose

As the system migrates from Node-RED to Golang, we lose the inherent visual representation that Node-RED's flow editor provided. These documents recreate that visual understanding through:

- **Mermaid diagrams** showing decision trees, state transitions, and data flows
- **Mapping tables** connecting inputs to outputs
- **Sequence diagrams** showing temporal behavior

## Documents

| Document | Description |
|----------|-------------|
| [DAY_PHASE_MODES.md](./DAY_PHASE_MODES.md) | How dayPhase drives music modes and lighting scenes |

## When to Update

Update these documents when:

1. **Plugin logic changes** - Any modification to decision-making in plugins
2. **New state variables** - Adding or removing state variables that affect flows
3. **New plugins** - Adding new plugins that have user-visible behavior
4. **Configuration changes** - Modifying how configs affect behavior

## Validation

All Mermaid diagrams in this directory are validated:

1. **Syntax validation**: `make validate-mermaid` checks diagram syntax
2. **GitHub rendering**: The Claude docs-review pipeline uses Playwright to verify diagrams render correctly on GitHub

## Creating New Flow Documents

When documenting a new automation flow:

1. Start with a high-level overview diagram
2. Break down complex logic into focused sub-diagrams
3. Include mapping tables for quick reference
4. Reference the relevant plugin source files
5. Run `make validate-mermaid` before committing

## Related Documentation

- [VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md) - System-wide architecture diagrams
- [ARCHITECTURE.md](../architecture/ARCHITECTURE.md) - Technical architecture details
- [PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md) - Plugin interfaces and lifecycle
