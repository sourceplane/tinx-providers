# setup-kubectl

Kiox provider for `kubectl`, modeled on the behavior of `Azure/setup-kubectl` but adapted to the current Kiox provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `KUBECTL_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `kubectl` on `PATH` after materialization.
- PATH mutation: handled by Kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: fetches `stable.txt`, `stable-<major>.<minor>.txt`, the platform binary, and the `.sha256` checksum over HTTPS.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`; `linux/arm` is also supported for the downloaded `kubectl` binary.

## Design

- Bundled installer tool: `setup-kubectl`
- User-facing tool: `kubectl`
- Workspace install path: `.workspace/tools/.../bin/kubectl` via Kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-kubectl`
- Version resolution:
  - `latest` or `stable` resolves through `stable.txt`
  - `1.30` resolves through `stable-1.30.txt`
  - `1.30.6` or `v1.30.6` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-kubectl \
  --dist dist \
  --output oci \
  --tag v0.1.0
```

The release command builds the bundled installer binary and produces an OCI image layout under `providers/setup-kubectl/oci`.

## Example workspace flow

```bash
cat > demo/kiox.yaml <<'EOF'
apiVersion: kiox.io/v1
kind: Workspace
workspace: demo
providers:
  kubectl:
    source: ghcr.io/sourceplane/kiox-setup-kubectl:v0.1.0
EOF

kiox init demo/kiox.yaml
KUBECTL_VERSION=v1.30.6 kiox -w demo exec -- kubectl version --client -o json
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
workspace: setup-kubectl-test
providers:
  kubectl:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir/kiox.yaml"
KUBECTL_VERSION=v1.30.6 kiox -w "$workspace_dir" exec -- kubectl version --client -o json
```

## Publishing

Push the provider to GHCR directly with `kiox release --push`:

```bash
cd providers/setup-kubectl
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-kubectl \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/kiox-setup-kubectl:v0.1.0
```

The GitHub Actions release workflow uses `sourceplane/kiox-release-action@v2` to run the same command and publish straight to GHCR.
