# setup-kustomize

Tinx provider for `kustomize`, modeled on `imranismail/setup-kustomize` while following the Tinx provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `KUSTOMIZE_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `kustomize` on `PATH` after materialization.
- PATH mutation: handled by Tinx workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` through the GitHub releases API, downloads the platform archive from the upstream GitHub release, and validates the official SHA256 published in `checksums.txt`.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`.

## Design

- Bundled installer tool: `setup-kustomize`
- User-facing tool: `kustomize`
- Workspace install path: `.workspace/tools/.../bin/kustomize` via Tinx local runtime
- Global cache: `~/.tinx/cache/providers/setup-kustomize`
- Version resolution:
  - `latest` or `stable` resolves through the latest GitHub release tag
  - `5.8.1` or `v5.8.1` installs that exact version

## Build And Package

```bash
go test ./...
tinx release \
  --manifest provider.yaml \
  --main ./cmd/tinx-provider-kustomize \
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
  kustomize:
    source: ghcr.io/sourceplane/tinx-setup-kustomize:v0.1.0
EOF

tinx init demo/tinx.yaml
KUSTOMIZE_VERSION=v5.8.1 tinx -w demo exec -- kustomize version
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/tinx.yaml" <<EOF
apiVersion: tinx.io/v1
kind: Workspace
workspace: setup-kustomize-test
providers:
  kustomize:
    source: $(pwd)/oci
EOF

tinx init "$workspace_dir/tinx.yaml"
KUSTOMIZE_VERSION=v5.8.1 tinx -w "$workspace_dir" exec -- kustomize version
```

## Publishing

```bash
cd providers/setup-kustomize
tinx release \
  --manifest provider.yaml \
  --main ./cmd/tinx-provider-kustomize \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/tinx-setup-kustomize:v0.1.0
```