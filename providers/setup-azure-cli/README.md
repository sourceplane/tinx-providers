# setup-azure-cli

Tinx provider for Azure CLI, modeled on the portable Azure CLI release archives while following the Tinx provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `AZURE_CLI_VERSION`, `AZ_VERSION`, or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `az` on `PATH` after materialization.
- PATH mutation: handled by Tinx workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` through the Azure CLI GitHub releases API, downloads the matching portable archive for the active platform, and validates the archive SHA256 using the digest published in the release asset metadata.
- Runtime requirement: the macOS portable archive still expects a local Python interpreter; the provider launcher auto-detects `python3.13`, then falls back to `python3` or `python` when `AZ_PYTHON` is unset.
- Platform coverage: `darwin` with `amd64` and `arm64`, and `windows/amd64`.

## Design

- Bundled installer tool: `setup-azure-cli`
- User-facing tool: `az` with an additional `azure-cli` alias
- Workspace install path: `.workspace/tools/.../bin/az` via Tinx local runtime
- Global cache: `~/.tinx/cache/providers/setup-azure-cli`
- Version resolution:
  - `latest` or `stable` resolves through the latest GitHub release tag
  - `2.85.0` or `v2.85.0` installs that exact version

## Build And Package

```bash
go test ./...
tinx release \
  --manifest provider.yaml \
  --main ./cmd/tinx-provider-azure-cli \
  --dist dist \
  --output oci \
  --tag v0.1.0
```

## Example workspace flow

```bash
cat > demo/tinx.yaml <<'EOF'
apiVersion: tinx.io/v1
kind: Workspace
workspace: demo
providers:
  az:
    source: ghcr.io/sourceplane/tinx-setup-azure-cli:v0.1.0
EOF

tinx init demo/tinx.yaml
AZURE_CLI_VERSION=2.85.0 tinx -w demo exec -- az version --output json
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/tinx.yaml" <<EOF
apiVersion: tinx.io/v1
kind: Workspace
workspace: setup-azure-cli-test
providers:
  az:
    source: $(pwd)/oci
EOF

tinx init "$workspace_dir/tinx.yaml"
AZURE_CLI_VERSION=2.85.0 tinx -w "$workspace_dir" exec -- az version --output json
```

## Publishing

```bash
cd providers/setup-azure-cli
tinx release \
  --manifest provider.yaml \
  --main ./cmd/tinx-provider-azure-cli \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/tinx-setup-azure-cli:v0.1.0
```