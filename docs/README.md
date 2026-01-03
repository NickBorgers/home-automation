# Documentation

This directory contains all project documentation, organized by audience and topic.

## Structure

```
docs/
├── architecture/           # System design
│   └── ARCHITECTURE.md     # Complete architecture, implementation status, roadmap
│
├── flows/                  # Automation flow documentation (visual logic)
│   ├── README.md           # Guide to automation flow documentation
│   └── DAY_PHASE_MODES.md  # Day phase, music mode, and lighting relationships
│
├── human/                  # Human-focused visual documentation
│   ├── VISUAL_ARCHITECTURE.md  # Mermaid diagrams of system architecture
│   └── DIAGRAM_QUICK_START.md  # Quick guide to navigating diagrams
│
├── reference/              # Technical reference (for engineers/AI agents)
│   ├── SHADOW_STATE.md     # Shadow state pattern for plugin observability
│   ├── PLUGIN_SYSTEM.md    # Plugin interfaces and lifecycle
│   ├── migration_mapping.md # State variable mapping from Node-RED to HA
│   └── CONCURRENCY_LESSONS.md # Concurrency patterns and lessons learned
│
├── operations/             # Deployment and process documentation
│   ├── DOCKER.md           # Docker build, run, and deployment guide
│   ├── BRANCH_PROTECTION.md # PR requirements and branch protection setup
│   └── CLAUDE_GHA_PIPELINES.md # Claude Code GitHub Actions documentation
│
└── archive/                # Historical documents (not required reading)
    ├── NODE_RED_TABS_ANALYSIS.md
    ├── MUSIC_FLOW_ANALYSIS.md
    ├── DIAGRAM_GENERATION_RULES.md
    └── SCENARIO_BASED_TESTING_PROPOSAL.md
```

## Quick Links

### For Engineers / AI Agents
Technical reference documentation for development:
- **[architecture/ARCHITECTURE.md](./architecture/ARCHITECTURE.md)** - Complete system design
- **[reference/SHADOW_STATE.md](./reference/SHADOW_STATE.md)** - Shadow state pattern (**READ BEFORE WRITING PLUGINS**)
- **[reference/PLUGIN_SYSTEM.md](./reference/PLUGIN_SYSTEM.md)** - Plugin interfaces
- **[reference/migration_mapping.md](./reference/migration_mapping.md)** - State variable mapping
- **[reference/CONCURRENCY_LESSONS.md](./reference/CONCURRENCY_LESSONS.md)** - Concurrency patterns

### Automation Flows
Visual documentation of automation behavior (replaces Node-RED visual flows):
- **[flows/README.md](./flows/README.md)** - Guide to automation flow documentation
- **[flows/DAY_PHASE_MODES.md](./flows/DAY_PHASE_MODES.md)** - Day phase, music, and lighting relationships

### For Human Developers
Visual documentation for understanding the system:
- **[human/VISUAL_ARCHITECTURE.md](./human/VISUAL_ARCHITECTURE.md)** - Mermaid diagrams
- **[human/DIAGRAM_QUICK_START.md](./human/DIAGRAM_QUICK_START.md)** - Diagram navigation guide

### Operations
Deployment and process documentation:
- **[operations/DOCKER.md](./operations/DOCKER.md)** - Docker deployment
- **[operations/BRANCH_PROTECTION.md](./operations/BRANCH_PROTECTION.md)** - PR requirements
- **[operations/CLAUDE_GHA_PIPELINES.md](./operations/CLAUDE_GHA_PIPELINES.md)** - Claude Code GitHub Actions pipelines

### Archive
Historical and proposal documents (not required reading):
- **[archive/](./archive/)** - Old analysis, future work proposals

## Root-Level Documentation

Some documentation files remain in the repository root because they are required there:
- **[CLAUDE.md](../CLAUDE.md)** - Claude Code project instructions (must be in root)
- **[AGENTS.md](../AGENTS.md)** - Development guide for AI agents (must be in root)
- **[README.md](../README.md)** - Project overview (standard location)

## Contributing

When adding new documentation:
1. Place it in the appropriate subdirectory based on its audience:
   - `flows/` for automation flow documentation with Mermaid diagrams
   - `human/` for visual, diagram-heavy docs
   - `reference/` for technical patterns and guides
   - `operations/` for deployment and process docs
   - `archive/` for historical or proposal documents
2. Update this README with a link to the new document
3. Update AGENTS.md if it's required reading for development
4. Follow markdown best practices (headers, links, code blocks)
5. For Mermaid diagrams, run `make validate-mermaid` before committing
