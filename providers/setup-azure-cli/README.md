# setup-azure-cli

kiox provider for Azure CLI, modeled on the portable Azure CLI release archives while following the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `AZURE_CLI_VERSION`, `AZ_VERSION`, or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `az` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` through the Azure CLI GitHub releases API, downloads the matching portable archive for the active platform, and validates the asset SHA256 using the digest published in the release asset metadata.
- Runtime requirement: the macOS portable archive still expects a local Python interpreter; the provider launcher auto-detects `python3.13`, then falls back to `python3` or `python` when `AZ_PYTHON` is unset.
- Platform coverage: `darwin` with `amd64` and `arm64`, and `windows/amd64`.
# setup-azure-cli

kiox provider for Azure CLI, modeled on the portable Azure CLI release archives while following the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `AZURE_CLI_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `az` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves an exact release from the Azure CLI GitHub releases API, downloads the platform archive from the release assets, and validates the asset SHA256 digest published alongside the release metadata.
- Platform coverage: `darwin` with `amd64` and `arm64`, plus `windows/amd64`.

## Design

- Bundled installer tool: `setup-azure-cli`
- User-facing tools: `az`, `azure-cli`
- Managed install path: `$KIOX_HOME/store/<storeID>/tools/az/bin/az` via the kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-azure-cli`
- Version resolution:
  - `latest` or `stable` resolves to the repository’s latest release
  - `2.85.0` or `v2.85.0` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-azure-cli \
  --dist dist \
  --output oci \
  --tag v0.2.0
```

## Example workspace flow

```bash
mkdir -p demo
cat > demo/kiox.yaml <<'EOF'
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: demo
providers:
  az:
    source: ghcr.io/sourceplane/setup-azure-cli:v0.2.0
EOF

kiox init demo
kiox --workspace demo ls
kiox --workspace demo status
AZURE_CLI_VERSION=2.85.0 kiox --workspace demo -- az version --output json
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: setup-azure-cli-test
providers:
  az:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir"
kiox --workspace "$workspace_dir" ls
kiox --workspace "$workspace_dir" status
AZURE_CLI_VERSION=2.85.0 kiox --workspace "$workspace_dir" -- az version --output json
```

## Publishing

```bash
cd providers/setup-azure-cli
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-azure-cli \
  --dist dist \
  --output oci \
  --tag v0.2.0 \
  --push ghcr.io/<org>/setup-azure-cli:v0.2.0
```