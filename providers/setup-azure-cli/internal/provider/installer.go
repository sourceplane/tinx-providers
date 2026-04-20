package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/sourceplane/kiox-providers/internal/installutil"
)

const (
	defaultRequestedVersion = "latest"
	defaultStableVersion    = "2.85.0"
	defaultToolName         = "az"
	defaultReleaseAPIBase   = "https://api.github.com/repos/Azure/azure-cli/releases"
	defaultReleasePageBase  = "https://github.com/Azure/azure-cli/releases"
	installedVersionFile    = ".kiox-azure-cli-version"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type Config struct {
	RequestedVersion string
	InstallDir       string
	TargetBin        string
	CacheDir         string
	KioxHome         string
	ToolName         string
	ReleaseAPIURLs   []string
	HTTPClient       *http.Client
	LogOutput        io.Writer
}

type Result struct {
	RequestedVersion string `json:"requested_version"`
	ResolvedVersion  string `json:"resolved_version"`
	BinaryPath       string `json:"binary_path"`
	SHA256           string `json:"sha256"`
	SourceURL        string `json:"source_url"`
	UsedCache        bool   `json:"used_cache"`
}

type Installer struct {
	client *http.Client
}

type releasePayload struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func NewInstaller() *Installer {
	return &Installer{client: installutil.NewHTTPClient()}
}

func URLsFromEnv(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls
}

func (i *Installer) Install(ctx context.Context, cfg Config) (Result, error) {
	client := i.client
	if cfg.HTTPClient != nil {
		client = cfg.HTTPClient
	}

	releaseAPIURLs, err := installutil.SanitizeURLs(cfg.ReleaseAPIURLs, []string{defaultReleaseAPIBase}, true)
	if err != nil {
		return Result{}, err
	}

	cacheDir, err := installutil.ResolveCacheDir(cfg.CacheDir, cfg.KioxHome, "setup-azure-cli")
	if err != nil {
		return Result{}, err
	}

	assetSuffix, archiveExt, launcherName, err := azureCLIPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	installDir, targetPath, err := resolveInstallPaths(cfg.InstallDir, cfg.TargetBin, launcherName)
	if err != nil {
		return Result{}, err
	}

	requested := strings.TrimSpace(cfg.RequestedVersion)
	if requested == "" {
		requested = defaultRequestedVersion
	}

	resolvedVersion, asset, err := resolveReleaseAsset(ctx, client, cacheDir, releaseAPIURLs, requested, assetSuffix, archiveExt)
	if err != nil {
		return Result{}, err
	}

	archivePath := archiveCachePath(cacheDir, resolvedVersion, asset.Name)
	usedCache := true
	if ok, err := installutil.FileMatchesChecksum(archivePath, asset.sha256()); err != nil {
		return Result{}, err
	} else if !ok {
		usedCache = false
		if _, err := installutil.DownloadToFile(ctx, client, []string{asset.BrowserDownloadURL}, archivePath, asset.sha256(), 0o644); err != nil {
			return Result{}, err
		}
	}

	versionFilePath := filepath.Join(installDir, installedVersionFile)
	if installedVersion, err := installutil.ReadTrimmedFile(versionFilePath); err == nil && installedVersion == resolvedVersion {
		if _, err := os.Stat(targetPath); err == nil {
			sha256, err := installutil.FileSHA256(targetPath)
			if err != nil {
				return Result{}, err
			}
			return Result{
				RequestedVersion: requested,
				ResolvedVersion:  resolvedVersion,
				BinaryPath:       targetPath,
				SHA256:           sha256,
				SourceURL:        asset.BrowserDownloadURL,
				UsedCache:        true,
			}, nil
		}
	}

	if err := installFromArchive(archivePath, installDir, archiveExt); err != nil {
		return Result{}, err
	}
	if err := ensureLauncherWrapper(installDir, targetPath, runtime.GOOS); err != nil {
		return Result{}, err
	}
	if err := installutil.WriteAtomic(versionFilePath, []byte(resolvedVersion+"\n"), 0o644); err != nil {
		return Result{}, err
	}

	sha256, err := installutil.FileSHA256(targetPath)
	if err != nil {
		return Result{}, err
	}

	return Result{
		RequestedVersion: requested,
		ResolvedVersion:  resolvedVersion,
		BinaryPath:       targetPath,
		SHA256:           sha256,
		SourceURL:        asset.BrowserDownloadURL,
		UsedCache:        usedCache,
	}, nil
}

func resolveReleaseAsset(ctx context.Context, client *http.Client, cacheDir string, releaseAPIURLs []string, requested, assetSuffix, archiveExt string) (string, releaseAsset, error) {
	requested = strings.TrimSpace(requested)
	latest := requested == "" || strings.EqualFold(requested, "latest") || strings.EqualFold(requested, "stable")
	cacheKey := "latest"
	version := ""
	if !latest {
		if !versionPattern.MatchString(requested) {
			return "", releaseAsset{}, fmt.Errorf("invalid version format %q; expected latest or major.minor.patch", requested)
		}
		version = normalizeExactVersion(requested)
		cacheKey = version
	}

	payload, err := loadOrFetchRelease(ctx, client, cacheDir, releaseAPIURLs, cacheKey, version, latest)
	if err != nil {
		if !latest {
			resolvedVersion, asset, fallbackErr := loadReleaseAssetFromPage(ctx, client, cacheDir, releaseAPIURLs, version, assetSuffix, archiveExt)
			if fallbackErr == nil {
				return resolvedVersion, asset, nil
			}
			return "", releaseAsset{}, fmt.Errorf("resolve Azure CLI release %s: %w; fallback from public release page failed: %v", version, err, fallbackErr)
		}
		return "", releaseAsset{}, err
	}
	resolvedVersion := releaseVersionFromTag(payload.TagName)
	if resolvedVersion == "" {
		return "", releaseAsset{}, fmt.Errorf("Azure CLI release tag %q did not contain a version", payload.TagName)
	}
	assetName := archiveFileName(resolvedVersion, assetSuffix, archiveExt)
	asset, err := assetByName(payload.Assets, assetName)
	if err != nil {
		return "", releaseAsset{}, err
	}
	if asset.sha256() == "" {
		return "", releaseAsset{}, fmt.Errorf("Azure CLI asset %s did not include a SHA256 digest", asset.Name)
	}
	return resolvedVersion, asset, nil
}

func loadOrFetchRelease(ctx context.Context, client *http.Client, cacheDir string, releaseAPIURLs []string, cacheKey, version string, latest bool) (releasePayload, error) {
	cachePath := filepath.Join(cacheDir, "releases", cacheKey+".json")
	if data, err := os.ReadFile(cachePath); err == nil {
		var payload releasePayload
		if err := json.Unmarshal(data, &payload); err == nil {
			return payload, nil
		}
	}

	urls := latestReleaseURLs(releaseAPIURLs)
	if !latest {
		urls = taggedReleaseURLs(releaseAPIURLs, version)
	}
	data, _, err := installutil.FetchFirst(ctx, client, urls)
	if err != nil {
		return releasePayload{}, err
	}
	var payload releasePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return releasePayload{}, fmt.Errorf("decode Azure CLI release metadata: %w", err)
	}
	if err := installutil.WriteAtomic(cachePath, data, 0o644); err != nil {
		return releasePayload{}, err
	}
	return payload, nil
}

func loadReleaseAssetFromPage(ctx context.Context, client *http.Client, cacheDir string, releaseAPIURLs []string, version, assetSuffix, archiveExt string) (string, releaseAsset, error) {
	assetName := archiveFileName(version, assetSuffix, archiveExt)
	pageURLs := taggedReleasePageURLs(releasePageBaseURLs(releaseAPIURLs), version)
	pageData, err := loadOrFetchReleasePage(ctx, client, cacheDir, version, pageURLs)
	if err != nil {
		return "", releaseAsset{}, err
	}
	checksum, err := checksumForAsset(pageData, assetName)
	if err != nil {
		return "", releaseAsset{}, err
	}
	downloadURLs := taggedReleaseDownloadURLs(releasePageBaseURLs(releaseAPIURLs), version, assetName)
	if len(downloadURLs) == 0 {
		return "", releaseAsset{}, fmt.Errorf("no Azure CLI release download URLs available for %s", version)
	}
	return version, releaseAsset{
		Name:               assetName,
		Digest:             "sha256:" + checksum,
		BrowserDownloadURL: downloadURLs[0],
	}, nil
}

func loadOrFetchReleasePage(ctx context.Context, client *http.Client, cacheDir, version string, pageURLs []string) ([]byte, error) {
	cachePath := filepath.Join(cacheDir, "releases", "pages", version+".html")
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	data, _, err := installutil.FetchFirst(ctx, client, pageURLs)
	if err != nil {
		return nil, err
	}
	if err := installutil.WriteAtomic(cachePath, data, 0o644); err != nil {
		return nil, err
	}
	return data, nil
}

func resolveInstallPaths(installDir, targetBin, launcherName string) (string, string, error) {
	trimmedInstallDir := strings.TrimSpace(installDir)
	trimmedTargetBin := strings.TrimSpace(targetBin)
	if trimmedInstallDir == "" && trimmedTargetBin == "" {
		return "", "", fmt.Errorf("KIOX_TARGET_TOOL_INSTALL_DIR or KIOX_TARGET_TOOL_BIN must be set")
	}

	if trimmedTargetBin != "" {
		absoluteTargetPath, err := filepath.Abs(trimmedTargetBin)
		if err != nil {
			return "", "", err
		}
		if trimmedInstallDir == "" {
			return filepath.Dir(filepath.Dir(absoluteTargetPath)), absoluteTargetPath, nil
		}
		absoluteInstallDir, err := filepath.Abs(trimmedInstallDir)
		if err != nil {
			return "", "", err
		}
		return absoluteInstallDir, absoluteTargetPath, nil
	}

	absoluteInstallDir, err := filepath.Abs(trimmedInstallDir)
	if err != nil {
		return "", "", err
	}
	return absoluteInstallDir, filepath.Join(absoluteInstallDir, "bin", launcherName), nil
}

func installFromArchive(archivePath, installDir, archiveExt string) error {
	parentDir := filepath.Dir(installDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return err
	}
	tempRoot, err := os.MkdirTemp(parentDir, ".extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)

	if archiveExt == "zip" {
		if err := installutil.ExtractZip(archivePath, tempRoot); err != nil {
			return err
		}
	} else {
		if err := installutil.ExtractTarGz(archivePath, tempRoot); err != nil {
			return err
		}
	}

	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("Azure CLI archive did not contain any files")
	}

	if err := os.RemoveAll(installDir); err != nil {
		return err
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(tempRoot, entry.Name()), filepath.Join(installDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func ensureLauncherWrapper(installDir, targetPath, goos string) error {
	if goos == "windows" {
		return nil
	}

	upstreamLauncherPath := filepath.Join(installDir, "libexec", "bin", defaultToolName)
	stat, err := os.Stat(upstreamLauncherPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if stat.IsDir() {
		return fmt.Errorf("expected Azure CLI launcher file at %s", upstreamLauncherPath)
	}

	wrapper := `#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ -z "${AZ_PYTHON:-}" ]; then
	for candidate in \
		/opt/homebrew/opt/python@3.13/libexec/bin/python3 \
		/usr/local/opt/python@3.13/libexec/bin/python3 \
		/opt/homebrew/bin/python3.13 \
		/usr/local/bin/python3.13 \
		python3 \
		python
	do
		case "$candidate" in
			/*)
				if [ -x "$candidate" ]; then
					export AZ_PYTHON="$candidate"
					break
				fi
				;;
			*)
				if resolved=$(command -v "$candidate" 2>/dev/null) && [ -n "$resolved" ] && [ -x "$resolved" ]; then
					export AZ_PYTHON="$resolved"
					break
				fi
				;;
		esac
	done
fi

exec "$SCRIPT_DIR/../libexec/bin/az" "$@"
`

	return installutil.WriteAtomic(targetPath, []byte(wrapper), 0o755)
}

func latestReleaseURLs(releaseAPIURLs []string) []string {
	return installutil.JoinURLPaths(releaseAPIURLs, "latest")
}

func taggedReleaseURLs(releaseAPIURLs []string, version string) []string {
	return installutil.JoinURLPaths(releaseAPIURLs, "tags/azure-cli-"+version)
}

func releasePageBaseURLs(releaseAPIURLs []string) []string {
	baseURLs := make([]string, 0, len(releaseAPIURLs))
	seen := map[string]struct{}{}
	for _, raw := range releaseAPIURLs {
		trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
		if trimmed == "" {
			continue
		}
		resolved := trimmed
		if parsed, err := url.Parse(trimmed); err == nil && strings.EqualFold(parsed.Host, "api.github.com") {
			segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(segments) >= 4 && segments[0] == "repos" {
				resolved = fmt.Sprintf("https://github.com/%s/%s/releases", segments[1], segments[2])
			}
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		baseURLs = append(baseURLs, resolved)
	}
	if len(baseURLs) == 0 {
		return []string{defaultReleasePageBase}
	}
	return baseURLs
}

func taggedReleasePageURLs(releasePageURLs []string, version string) []string {
	return installutil.JoinURLPaths(releasePageURLs, "tag/azure-cli-"+version)
}

func taggedReleaseDownloadURLs(releasePageURLs []string, version, assetName string) []string {
	return installutil.JoinURLPaths(releasePageURLs, "download/azure-cli-"+version+"/"+assetName)
}

func checksumForAsset(pageData []byte, assetName string) (string, error) {
	pattern := regexp.MustCompile(`(?i)([a-f0-9]{64})\s+` + regexp.QuoteMeta(assetName))
	matches := pattern.FindSubmatch(pageData)
	if len(matches) != 2 {
		return "", fmt.Errorf("Azure CLI release page checksum for %s not found", assetName)
	}
	return strings.ToLower(string(matches[1])), nil
}

func assetByName(assets []releaseAsset, name string) (releaseAsset, error) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("Azure CLI release asset %s not found", name)
}

func archiveFileName(version, assetSuffix, archiveExt string) string {
	return fmt.Sprintf("azure-cli-%s-%s.%s", version, assetSuffix, archiveExt)
}

func archiveCachePath(cacheDir, version, archiveName string) string {
	return filepath.Join(cacheDir, "archives", version, archiveName)
}

func azureCLIPlatform(goos, goarch string) (string, string, string, error) {
	switch goos {
	case "darwin":
		switch goarch {
		case "amd64":
			return "macos-x86_64", "tar.gz", defaultToolName, nil
		case "arm64":
			return "macos-arm64", "tar.gz", defaultToolName, nil
		}
	case "windows":
		switch goarch {
		case "amd64":
			return "x64", "zip", defaultToolName + ".cmd", nil
		}
	}
	return "", "", "", fmt.Errorf("unsupported platform %q/%q", goos, goarch)
}

func normalizeExactVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func releaseVersionFromTag(tag string) string {
	trimmed := strings.TrimSpace(tag)
	trimmed = strings.TrimPrefix(trimmed, "azure-cli-")
	if !versionPattern.MatchString(trimmed) {
		return ""
	}
	return trimmed
}

func (a releaseAsset) sha256() string {
	if !strings.HasPrefix(a.Digest, "sha256:") {
		return ""
	}
	return strings.TrimPrefix(a.Digest, "sha256:")
}
