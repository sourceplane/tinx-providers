# kiox-providers

Monorepo for kiox setup-style providers built around the current normalized provider package model.

## Layout

- `internal/installutil`: Shared archive, download, and checksum helpers used by provider installers.
- `providers/setup-azure-cli`: kiox provider for `az`, backed by a bundled Go installer.
- `providers/setup-gcloud`: kiox provider for `gcloud`, backed by a bundled Go installer that materializes the Cloud SDK.
- `providers/setup-helm`: kiox provider for `helm`, backed by a bundled Go installer.
- `providers/setup-kustomize`: kiox provider for `kustomize`, backed by a bundled Go installer.
- `providers/setup-kubectl`: kiox provider for `kubectl`, backed by a bundled Go installer.
- `providers/setup-terraform`: kiox provider for `terraform`, backed by a bundled Go installer.

## Common flows

Validate a provider:

```bash
cd providers/<provider>
go test ./...
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-<tool> \
  --dist dist \
  --output oci \
  --tag validate
```

Publish a provider:

```bash
kiox release \
  --manifest provider.yaml \
  --main ./cmd/kiox-provider-<tool> \
  --dist dist \
  --output oci \
  --tag v0.2.0 \
  --push ghcr.io/<org>/setup-<tool>:v0.2.0
```

Run a local workspace check:

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/kiox.yaml" <<EOF
apiVersion: kiox.io/v1
kind: Workspace
metadata:
  name: demo
providers:
  helm:
    source: $(pwd)/providers/setup-helm/oci
EOF

kiox init "$workspace_dir"
kiox --workspace "$workspace_dir" ls
kiox --workspace "$workspace_dir" status
HELM_VERSION=v4.1.4 kiox --workspace "$workspace_dir" -- helm version --template '{{ .Version }}'
```

## CI model

The GitHub Actions setup is intentionally minimal:

- `ci.yml` runs a provider matrix over every checked-in provider on the runner each provider requires, executes `go test`, validates `kiox release` with `sourceplane/kiox-release-action@v1`, and smoke-tests both transient providers and workspace manifests through `sourceplane/kiox-action@v2`.
- `release.yml` publishes any provider tagged as `providers/<provider>/v*` with `sourceplane/kiox-release-action@v1`.
