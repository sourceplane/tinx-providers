# setup-kubectl

kiox provider for `kubectl`, modeled on the behavior of `Azure/setup-kubectl` while following the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `KUBECTL_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `kubectl` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: fetches `stable.txt`, `stable-<major>.<minor>.txt`, the platform binary, and the `.sha256` checksum over HTTPS.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`; `linux/arm` is also supported for the downloaded `kubectl` binary.

## Design

- Bundled installer tool: `setup-kubectl`
- User-facing tool: `kubectl`
- Managed install path: `$KIOX_HOME/store/<storeID>/tools/kubectl/bin/kubectl` via the kiox local runtime
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
  --tag v0.2.0
```

The release command builds the bundled installer binary and produces an OCI image layout under `providers/setup-kubectl/oci`.

## Example workspace flow

```bash
mkdir -p demo
cat > demo/kiox.yaml <<'EOF'
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: demo
providers:
  kubectl:
    source: ghcr.io/sourceplane/setup-kubectl:v0.2.0
EOF

kiox init demo
kiox --workspace demo ls
kiox --workspace demo status
KUBECTL_VERSION=v1.30.6 kiox --workspace demo -- kubectl version --client -o json
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: setup-kubectl-test
providers:
  kubectl:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir"
kiox --workspace "$workspace_dir" ls
kiox --workspace "$workspace_dir" status
KUBECTL_VERSION=v1.30.6 kiox --workspace "$workspace_dir" -- kubectl version --client -o json
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
  --tag v0.2.0 \
  --push ghcr.io/<org>/setup-kubectl:v0.2.0
```

The GitHub Actions release workflow installs `kiox` with `go install github.com/sourceplane/kiox/cmd/kiox@latest` and runs the same command directly to publish straight to GHCR.
