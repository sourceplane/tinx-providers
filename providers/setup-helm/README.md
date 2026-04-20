# setup-helm

Kiox provider for `helm`, modeled on `Azure/setup-helm` while following the Kiox provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `HELM_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `helm` on `PATH` after materialization.
- PATH mutation: handled by Kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` via `https://get.helm.sh/helm-latest-version`, downloads the platform archive from `https://get.helm.sh`, and validates the official archive SHA256.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`; `linux/arm` and `linux/386` archives are also understood when the installer runs on those platforms.

## Design

- Bundled installer tool: `setup-helm`
- User-facing tool: `helm`
- Workspace install path: `.workspace/tools/.../bin/helm` via Kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-helm`
- Version resolution:
  - `latest` or `stable` resolves through `helm-latest-version`
  - `4.1.4` or `v4.1.4` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-helm \
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
  helm:
    source: ghcr.io/sourceplane/kiox-setup-helm:v0.1.0
EOF

kiox init demo/kiox.yaml
HELM_VERSION=v4.1.4 kiox -w demo exec -- helm version --template '{{ .Version }}'
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
workspace: setup-helm-test
providers:
  helm:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir/kiox.yaml"
HELM_VERSION=v4.1.4 kiox -w "$workspace_dir" exec -- helm version --template '{{ .Version }}'
```

## Publishing

```bash
cd providers/setup-helm
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-helm \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/kiox-setup-helm:v0.1.0
```