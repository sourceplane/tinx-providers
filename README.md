# tinx-providers

Monorepo for Tinx providers built around the current normalized package model.

## Layout

- `internal/installutil`: Shared archive, download, and checksum helpers used by provider installers.
- `providers/setup-azure-cli`: Tinx provider for `az`, backed by a bundled Go installer.
- `providers/setup-gcloud`: Tinx provider for `gcloud`, backed by a bundled Go installer that materializes the Cloud SDK.
- `providers/setup-helm`: Tinx provider for `helm`, backed by a bundled Go installer.
- `providers/setup-kustomize`: Tinx provider for `kustomize`, backed by a bundled Go installer.
- `providers/setup-kubectl`: Tinx provider for `kubectl`, backed by a bundled Go installer.
- `providers/setup-terraform`: Tinx provider for `terraform`, backed by a bundled Go installer.

## Common flows

Validate a provider:

```bash
cd providers/<provider>
go test ./...
tinx release --manifest provider.yaml --main ./cmd/tinx-provider-<tool> --dist dist --output oci --tag validate
```

Publish a provider:

```bash
tinx release --manifest provider.yaml --main ./cmd/tinx-provider-<tool> --dist dist --output oci --tag v0.1.0 --push ghcr.io/<org>/tinx-setup-<tool>:v0.1.0
```

Run a local workspace check:

```bash
workspace_dir=$(mktemp -d)
cat > "$workspace_dir/tinx.yaml" <<EOF
apiVersion: tinx.io/v1
kind: Workspace
workspace: demo
providers:
  helm:
    source: $(pwd)/providers/setup-helm/oci
EOF

tinx init "$workspace_dir/tinx.yaml"
HELM_VERSION=v4.1.4 tinx -w "$workspace_dir" exec -- helm version --template '{{ .Version }}'
```

## CI model

The GitHub Actions setup is intentionally minimal:

- `ci.yml` runs a provider matrix over every checked-in provider on the runner each provider requires, executes `go test`, validates `tinx release` with `sourceplane/tinx-release-action@v2`, and smoke-tests both transient providers and workspace manifests through `sourceplane/tinx-action@v2`.
- `release.yml` publishes any provider tagged as `providers/<provider>/v*` with `sourceplane/tinx-release-action@v2`.
