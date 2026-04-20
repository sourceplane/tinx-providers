gpt54m "

You are a senior platform engineer building CNCF-grade tooling.

Goal:
Create a production-ready kiox provider that replicates the behavior of a GitHub Action setup tool but follows kiox architecture (provider package + runtime + workspace separation).

---

## Context

Kiox docs:
- local repository docs under `../kiox/website/docs/`
- CLI behavior in `../kiox/README.md` and `../kiox/TEST_PROVIDERS.md`

Reference GHA Action:
{{GHA_REPO_URL}}

Provider Name:
{{KIOX_PROVIDER_NAME}}

Target Tool:
{{TOOL_NAME}}

Language:
Go (preferred for portability and static binaries)

---

## 🧱 Phase 1 — Understand & Design

1. Read the reference GitHub Action codebase and extract:
   - Inputs (version, path, config, auth)
   - Outputs
   - Environment modifications (PATH, env vars)
   - Download/install strategy
   - Platform differences (linux/mac/windows)

2. Map this into kiox concepts:
   - Provider package schema
   - Runtime execution model
   - Workspace mutation (files, binaries, env)

3. Design:
   - provider.yaml (kiox.io/v1 schema)
   - runtime contract (idempotent execution)
   - caching strategy (avoid re-downloads)
   - version resolution strategy (latest/stable/explicit)

---

## Phase 2 — Implementation (Go)

Implement:

1. CLI entrypoint:
   - kiox-provider-{{TOOL_NAME}}

2. Features:
   - Download tool binary securely (HTTPS + checksum validation)
   - Materialize the target tool under the kiox provider store when invoked through `install.tool`
   - Expose the command through workspace shims and `provides`
   - Support version pinning

3. Cross-platform support:
   - Detect OS/ARCH
   - Fetch correct binary

4. Security:
   - Verify SHA256 checksums
   - Avoid executing remote scripts
   - No shell injection risks

---

## Phase 3 — Kiox Integration

1. Create provider.yaml:
   - `apiVersion: kiox.io/v1`
   - `kind: Provider`
   - provider metadata (`namespace`, `name`, `version`, `description`)
   - setup-style tools: bundled installer tool plus default local tool

2. Ensure compatibility with:
   - `kiox release`
   - `kiox init`
   - `kiox ls`
   - `kiox status`
   - `kiox --workspace <dir> -- <command>`

3. Follow the normalized provider package model documented in kiox.

---

## Phase 4 — Local Testing

1. Install kiox locally:
   curl -fsSL https://raw.githubusercontent.com/sourceplane/kiox/main/install.sh | bash

2. Create a test workspace with `kiox.yaml`

3. Run:
   kiox release --manifest provider.yaml --main ./cmd/kiox-provider-{{TOOL_NAME}} --dist dist --output oci
   kiox init demo -p ./oci as {{TOOL_NAME}}

4. Validate:
   - `kiox ls` shows the provider and tool inventory
   - `kiox status` reports the provider as ready or lazy as expected
   - the tool runs (`{{TOOL_NAME}} version`)

---

## Phase 5 — Packaging

1. Package provider:
   - OCI image layout via `kiox release`
   - versioned registry release via `--push`

2. Ensure:
   - reproducible builds
   - minimal binary size

---

## Phase 6 — Enhancements

- Add caching layer (~/.kiox/cache/providers)
- Add semantic version resolution
- Add fallback mirrors (GitHub releases, official mirrors)
- Add logging + debug mode

---

## Output

Produce:

1. Full Go implementation
2. provider.yaml
3. Example kiox workspace usage
4. Test script
5. Packaging instructions

---

## Constraints

- No bash-heavy logic (Go-first)
- No unsafe curl | sh patterns
- Deterministic installs only
- Idempotent execution

---

Now generate the full implementation.

"
