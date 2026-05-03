# stackr — Claude Guidelines

## Build & Test

```bash
make install        # Install dev tools (templ, hamr)
# Migrations run automatically when the daemon starts
hamr dev            # Run dev server (file watching, builds, live reload)
make build          # Build CLI + daemon binaries (generates templ first)
make test           # Run unit tests
make test-integration  # Run integration tests (requires Docker)
make lint           # Run linters
make templint       # Lint .templ files for silent failures and a11y issues
```

## Project Conventions

See AGENTS.md for all project conventions, patterns, and guides.

## Framework Reference

See `docs/hamr-reference/llms.txt` for a compact HAMR API reference.
See `docs/hamr-reference/llms-full.txt` for complete package documentation.
