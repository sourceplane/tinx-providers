gpt54m "

You are a senior platform engineer building CNCF-grade tooling.

Goal:
Create a production-ready Kiox provider that replicates the behavior of a GitHub Action setup tool but follows Kiox architecture (provider + runtime + workspace separation).

---

## 📌 Context

Kiox Docs:
https://docs.kiox.sourceplane.ai

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

2. Map this into Kiox concepts:
   - Provider schema (inputs/outputs)
   - Runtime execution model
   - Workspace mutation (files, binaries, env)

3. Design:
   - provider.yaml (k8s-style schema)
   - runtime contract (idempotent execution)
   - caching strategy (avoid re-downloads)
   - version resolution strategy (latest/stable/explicit)

---

## ⚙️ Phase 2 — Implementation (Go)

Implement:

1. CLI entrypoint:
   - kiox-provider-{{TOOL_NAME}}

2. Features:
   - Download tool binary securely (HTTPS + checksum validation)
   - Extract/install to workspace (.kiox/tools/{{tool}})
   - Add to PATH for workspace runtime
   - Support version pinning

3. Cross-platform support:
   - Detect OS/ARCH
   - Fetch correct binary

4. Security:
   - Verify SHA256 checksums
   - Avoid executing remote scripts
   - No shell injection risks

---

## 📦 Phase 3 — Kiox Integration

1. Create provider.yaml:
   - name: {{KIOX_PROVIDER_NAME}}
   - inputs:
     - version
   - outputs:
     - binary_path

2. Ensure compatibility with:
   - kiox workspace
   - kiox runtime execution

3. Follow k8s-style declarative schema (CRD-like)

---

## 🧪 Phase 4 — Local Testing

1. Install kiox locally:
   curl -fsSL https://raw.githubusercontent.com/sourceplane/kiox/main/install.sh | bash

2. Create test workspace

3. Run:
   kiox apply -f provider.yaml

4. Validate:
   - binary exists
   - tool runs ({{TOOL_NAME}} version)
   - PATH updated correctly

---

## 📦 Phase 5 — Packaging

1. Package provider:
   - OCI-compatible artifact (if supported)
   - versioned release

2. Ensure:
   - reproducible builds
   - minimal binary size

---

## 🚀 Phase 6 — Enhancements

- Add caching layer (~/.kiox/cache)
- Add semantic version resolution
- Add fallback mirrors (GitHub releases, official mirrors)
- Add logging + debug mode

---

## 🧾 Output

Produce:

1. Full Go implementation
2. provider.yaml
3. Example workspace usage
4. Test script
5. Packaging instructions

---

## ⚠️ Constraints

- No bash-heavy logic (Go-first)
- No unsafe curl | sh patterns
- Deterministic installs only
- Idempotent execution

---

Now generate the full implementation.

"
