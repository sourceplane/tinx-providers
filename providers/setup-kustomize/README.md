# setup-kustomize

Kiox provider for `kustomize`, modeled on `imranismail/setup-kustomize` while following the Kiox provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `KUSTOMIZE_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `kustomize` on `PATH` after materialization.
- PATH mutation: handled by Kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` through the GitHub releases API, downloads the platform archive from the upstream GitHub release, and validates the official SHA256 published in `checksums.txt`.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`.

## Design

- Bundled installer tool: `setup-kustomize`
- User-facing tool: `kustomize`
- Workspace install path: `.workspace/tools/.../bin/kustomize` via Kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-kustomize`
- Version resolution:
  - `latest` or `stable` resolves through the latest GitHub release tag
  - `5.8.1` or `v5.8.1` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-kustomize \
  --dist dist \
  --output oci \
  --tag v0.1.0
```

## Example workspace flow

```bash
cat > demo/kiox.yaml <<'EOF'
apiVersion: kiox.io/v1
kind: Workspace
workspace: demo
providers:
  kustomize:
    source: ghcr.io/sourceplane/kiox-setup-kustomize:v0.1.0
EOF

kiox init demo/kiox.yaml
KUSTOMIZE_VERSION=v5.8.1 kiox -w demo exec -- kustomize version
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
workspace: setup-kustomize-test
providers:
  kustomize:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir/kiox.yaml"
KUSTOMIZE_VERSION=v5.8.1 kiox -w "$workspace_dir" exec -- kustomize version
```

## Publishing

```bash
cd providers/setup-kustomize
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-kustomize \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/kiox-setup-kustomize:v0.1.0
```