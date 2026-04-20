# setup-terraform

Kiox provider for `terraform`, modeled on `hashicorp/setup-terraform` while following the Kiox provider/runtime/workspace split.

## Behavior mapping

- Input: version pinning is accepted through `TERRAFORM_VERSION` or `INPUT_VERSION`.
- Output equivalent: the installer prints `binary_path`, and the workspace runtime resolves `terraform` on `PATH` after materialization.
- PATH mutation: handled by Kiox workspace shims and the local tool runtime, not by the provider binary itself.
- Download strategy: resolves `latest` from the HashiCorp Terraform release index, downloads the platform zip from `releases.hashicorp.com`, and validates the archive checksum from the official `SHA256SUMS` file.
- Platform coverage: `darwin` and `linux` with `amd64` and `arm64`, `linux/arm`, and `windows/amd64`.

## Design

- Bundled installer tool: `setup-terraform`
- User-facing tool: `terraform`
- Workspace install path: `.workspace/tools/.../bin/terraform` via Kiox local runtime
- Global cache: `~/.kiox/cache/providers/setup-terraform`
- Version resolution:
  - `latest` or `stable` resolves through the HashiCorp release index
  - `1.14.8` or `v1.14.8` installs that exact version

## Build And Package

```bash
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-terraform \
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
  terraform:
    source: ghcr.io/sourceplane/kiox-setup-terraform:v0.1.0
EOF

kiox init demo/kiox.yaml
TERRAFORM_VERSION=1.14.8 kiox -w demo exec -- terraform version
```

## Local validation

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
workspace: setup-terraform-test
providers:
  terraform:
    source: $(pwd)/oci
EOF

kiox init "$workspace_dir/kiox.yaml"
TERRAFORM_VERSION=1.14.8 kiox -w "$workspace_dir" exec -- terraform version
```

## Publishing

```bash
cd providers/setup-terraform
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-terraform \
  --dist dist \
  --output oci \
  --tag v0.1.0 \
  --push ghcr.io/<org>/kiox-setup-terraform:v0.1.0
```