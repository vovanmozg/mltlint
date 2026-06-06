# mltlint — project instructions

CLI that scans an `unsorted` dir, finds visual duplicates of images in an
`originals` dir (perceptual-hash based, rotation-aware), and emits a JSON
report + a shell script to move the duplicates.

## Versioning & build (required on every code change)

When you change code (anything under `cmd/` or `internal/`), you MUST:

1. **Bump the version** following semver:
   - patch (x.y.**Z**) — bug fix, no behavior change
   - minor (x.**Y**.0) — new backward-compatible feature
   - major (**X**.0.0) — breaking change
   The version string lives in [internal/report/json.go](internal/report/json.go)
   (the `"version"` field of the report header). Update it, and create a matching
   annotated git tag `vX.Y.Z` (`git tag -a vX.Y.Z -m "..."`). Pushing the tag
   triggers the GitHub release workflow.

2. **Rebuild** the binary:
   ```bash
   make build      # CGO_ENABLED=0 go build -o bin/mltlint ./cmd/mltlint
   ```
   The build is pure Go (no cgo) → a single static binary.

Docs-only changes (README, docs/, this file) do not need a version bump.

## Tests

```bash
make test         # go test -race -cover ./...
```

Every requirement in [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) maps to a test;
keep them in sync when adding behavior.
