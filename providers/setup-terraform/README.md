# setup-terraform

kiox provider for `terraform`, modeled on `hashicorp/setup-terraform` while following the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `TERRAFORM_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `terraform` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` via the HashiCorp release index, downloads the versioned archive from `releases.hashicorp.com`, and validates the official SHA256SUMS entry.
- Platform coverage: `darwin` and `linux` with `amd64` and `arm64`, `linux/arm`, and `windows/amd64`.
# setup-terraform

kiox provider for `terraform`, modeled on `hashicorp/setup-terraform` while following the current kiox provider package, runtime, and workspace model.

## Behavior mapping

- Input: version pinning is accepted through `TERRAFORM_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and kiox resolves `terraform` on `PATH` after lazy materialization.
- PATH mutation: handled by kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` via the HashiCorp release index, downloads the versioned archive from `releases.hashicorp.com`, and validates the official SHA256SUMS entry.
- Platform coverage: `linux`, `darwin`, and `windows` with `amd64` and `arm64`; `linux/arm` is also supported when HashiCorp publishes that archive.

## Design

- Bundled installer tool: `setup-terraform`
- User-facing tool: `terraform`
- Managed install path: `$KIOX_HOME/store/<storeID>/tools/terraform/bin/terraform` via the kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-terraform`
- Version resolution:
  - `latest` or `stable` resolves through the release index
  - `1.14.8` or `v1.14.8` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-terraform \
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
  terraform:
    source: ghcr.io/sourceplane/setup-terraform:v0.2.0
EOF

kiox init demo
kiox --workspace demo ls
kiox --workspace demo status
TERRAFORM_VERSION=1.14.8 kiox --workspace demo -- terraform version -json
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: setup-terraform-test
providers:
  terraform:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir"
kiox --workspace "$workspace_dir" ls
kiox --workspace "$workspace_dir" status
TERRAFORM_VERSION=1.14.8 kiox --workspace "$workspace_dir" -- terraform version -json
```

## Publishing

```bash
cd providers/setup-terraform
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-terraform \
  --dist dist \
  --output oci \
  --tag v0.2.0 \
  --push ghcr.io/<org>/setup-terraform:v0.2.0
```