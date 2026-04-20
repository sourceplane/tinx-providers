# setup-kustomize

kiox provider for `kustomize`, modeled on `imranismail/setup-kustomize` while packaging the upstream `kubernetes-sigs/kustomize` release artifacts in the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `KUSTOMIZE_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `kustomize` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` through the GitHub releases API, downloads the platform archive from the upstream GitHub release, and validates the official SHA256 published in `checksums.txt`.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`.
# setup-kustomize

kiox provider for `kustomize`, modeled on `kubernetes-sigs/kustomize` release artifacts while following the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `KUSTOMIZE_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `kustomize` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` through the GitHub releases API, downloads the official archive from the release download path, and validates the published checksum from the same release.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`.

## Design

- Bundled installer tool: `setup-kustomize`
- User-facing tool: `kustomize`
- Managed install path: `$KIOX_HOME/store/<storeID>/tools/kustomize/bin/kustomize` via the kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-kustomize`
- Version resolution:
  - `latest` or `stable` resolves through the latest release API
  - `5.8.1` or `v5.8.1` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-kustomize \
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
  kustomize:
    source: ghcr.io/sourceplane/setup-kustomize:v0.2.0
EOF

kiox init demo
kiox --workspace demo ls
kiox --workspace demo status
KUSTOMIZE_VERSION=v5.8.1 kiox --workspace demo -- kustomize version
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: setup-kustomize-test
providers:
  kustomize:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir"
kiox --workspace "$workspace_dir" ls
kiox --workspace "$workspace_dir" status
KUSTOMIZE_VERSION=v5.8.1 kiox --workspace "$workspace_dir" -- kustomize version
```

## Publishing

```bash
cd providers/setup-kustomize
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-kustomize \
  --dist dist \
  --output oci \
  --tag v0.2.0 \
  --push ghcr.io/<org>/setup-kustomize:v0.2.0
```