# setup-helm

Tinx provider for `helm`, modeled on `Azure/setup-helm` while following the Tinx provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `HELM_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `helm` on `PATH` after materialization.
- PATH mutation: handled by Tinx workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` via `https://get.helm.sh/helm-latest-version`, downloads the platform archive from `https://get.helm.sh`, and validates the official archive SHA256.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`; `linux/arm` and `linux/386` archives are also understood when the installer runs on those platforms.

## Design

- Bundled installer tool: `setup-helm`
- User-facing tool: `helm`
- Workspace install path: `.workspace/tools/.../bin/helm` via Tinx local runtime
- Global cache: `~/.tinx/cache/providers/setup-helm`
- Version resolution:
  - `latest` or `stable` resolves through `helm-latest-version`
  - `4.1.4` or `v4.1.4` installs that exact version

## Build And Package

```bash
go test ./...
tinx release \
  --manifest provider.yaml \
  --main ./cmd/tinx-provider-helm \
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
  helm:
    source: ghcr.io/sourceplane/tinx-setup-helm:v0.1.0
EOF

tinx init demo/tinx.yaml
HELM_VERSION=v4.1.4 tinx -w demo exec -- helm version --template '{{ .Version }}'
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/tinx.yaml" <<EOF
apiVersion: tinx.io/v1
kind: Workspace
workspace: setup-helm-test
providers:
  helm:
    source: $(pwd)/oci
EOF

tinx init "$workspace_dir/tinx.yaml"
HELM_VERSION=v4.1.4 tinx -w "$workspace_dir" exec -- helm version --template '{{ .Version }}'
```

## Publishing

```bash
cd providers/setup-helm
tinx release \
  --manifest provider.yaml \
  --main ./cmd/tinx-provider-helm \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/tinx-setup-helm:v0.1.0
```