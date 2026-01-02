# Publishing the Go Client

This document describes how to version and publish the AIQA Go client module.

## Version Management

The Go client uses semantic versioning and follows Go module conventions:

- **Version format**: `vMAJOR.MINOR.PATCH` (e.g., `v0.4.1`)
- **Version tracking**: 
  - `version.json` - Contains version, git commit, and date (managed by `set-version-json.sh` in repo root)
  - Git tags - Used by Go modules for version resolution

## Publishing Process

### 1. Update Version

From the repository root, run the version update script:

```bash
# From repo root (not client-go directory)
./set-version-json.sh
```

This script:
- Updates `version.json` in `client-go/version.json`
- Updates version in other components (Python, JS, server, etc.)
- Uses the version defined at the top of the script

### 2. Commit Changes

```bash
git add version.json client-go/version.json
git commit -m "Bump version to v0.4.2"
```

### 3. Create and Push Git Tag

Go modules use git tags for versioning. The tag must:
- Start with `v` (e.g., `v0.4.1`, not `0.4.1`)
- Follow semantic versioning
- Match the version in `version.json` (without the `v` prefix)

```bash
# Create the tag
git tag v0.4.2

# Push the tag to GitHub
git push origin v0.4.2

# Or push all tags
git push --tags
```

### 4. Verify Publication

1. **Check GitHub tags:**
   - Visit: https://github.com/winterstein/aiqa/tags
   - Verify the new tag appears

2. **Test installation:**
   ```bash
   # In a test project
   go get github.com/winterstein/aiqa/client-go@v0.4.2
   ```

3. **Check Go proxy:**
   - The Go proxy (proxy.golang.org) automatically indexes public GitHub repositories
   - It may take a few minutes for the new version to appear
   - Check: https://pkg.go.dev/github.com/winterstein/aiqa/client-go

## Module Path

**Module path:** `github.com/winterstein/aiqa/client-go`

**Repository location:** `github.com/winterstein/aiqa`

The module path matches the repository location, so no redirects are needed. Users install with:
```bash
go get github.com/winterstein/aiqa/client-go
```

## Pre-release Versions

For alpha/beta releases, use pre-release tags:

```bash
git tag v0.4.2-alpha.1
git tag v0.4.2-beta.1
git push origin v0.4.2-alpha.1
```

Users can install pre-release versions:
```bash
go get github.com/winterstein/aiqa/client-go@v0.4.2-alpha.1
```

## Major Version Updates

For major version changes (v1.0.0, v2.0.0, etc.), you may need to:

1. **Update module path** (for v2+):
   ```go
   // In go.mod
   module github.com/winterstein/aiqa/client-go/v2
   ```

2. **Update import paths** in the codebase

3. **Create a new major version tag**

See [Go modules documentation](https://go.dev/doc/modules/major-version) for details.

## Troubleshooting

### Tag not appearing in Go proxy

- Wait a few minutes - the Go proxy indexes repositories asynchronously
- Verify the tag is pushed to GitHub
- Check that the repository is public
- Try: `GOPROXY=direct go get github.com/winterstein/aiqa/client-go@v0.4.2`

### Module path issues

If users get "module not found" errors:
- Verify the module path in `go.mod` matches the repository structure
- Check that the repository is public
- Ensure git tags are properly formatted (must start with `v`)

### Version conflicts

If there are version conflicts:
- Use `go mod tidy` to clean up dependencies
- Check for conflicting tags (e.g., both `v0.4.1` and `0.4.1`)

## Best Practices

1. **Always tag from the main branch** (or the branch you want to release)
2. **Test before tagging** - ensure the code compiles and tests pass
3. **Use annotated tags** (default with `git tag`):
   ```bash
   git tag -a v0.4.2 -m "Release v0.4.2"
   ```
4. **Keep version.json in sync** with git tags
5. **Document breaking changes** in release notes

## Automated Publishing (Future)

Consider setting up GitHub Actions to:
- Automatically create tags from version.json
- Run tests before publishing
- Publish to Go proxy
- Create GitHub releases

Example workflow (`.github/workflows/publish-go.yml`):
```yaml
name: Publish Go Module
on:
  push:
    tags:
      - 'v*'
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      - run: go test ./...
      - run: go mod verify
```

