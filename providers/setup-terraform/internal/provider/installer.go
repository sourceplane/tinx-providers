package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/sourceplane/kiox-providers/internal/installutil"
)

const (
	defaultRequestedVersion = "latest"
	defaultStableVersion    = "1.14.8"
	defaultToolName         = "terraform"
	defaultDownloadBaseURL  = "https://releases.hashicorp.com/terraform"
	defaultIndexURL         = "https://releases.hashicorp.com/terraform/index.json"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type Config struct {
	RequestedVersion string
	InstallDir       string
	TargetBin        string
	CacheDir         string
	KioxHome         string
	ToolName         string
	DownloadMirrors  []string
	IndexURLs        []string
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

type releaseIndexPayload struct {
	Versions map[string]json.RawMessage `json:"versions"`
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

	downloadBaseURLs, err := installutil.SanitizeURLs(cfg.DownloadMirrors, []string{defaultDownloadBaseURL}, true)
	if err != nil {
		return Result{}, err
	}
	indexURLs, err := installutil.SanitizeURLs(cfg.IndexURLs, []string{defaultIndexURL}, true)
	if err != nil {
		return Result{}, err
	}

	cacheDir, err := installutil.ResolveCacheDir(cfg.CacheDir, cfg.KioxHome, "setup-terraform")
	if err != nil {
		return Result{}, err
	}

	toolName := strings.TrimSpace(cfg.ToolName)
	if toolName == "" {
		toolName = defaultToolName
	}

	targetPath, err := installutil.ResolveTargetPath(cfg.TargetBin, cfg.InstallDir, toolName, runtime.GOOS)
	if err != nil {
		return Result{}, err
	}

	goos, goarch, archiveExt, err := terraformPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	requested := strings.TrimSpace(cfg.RequestedVersion)
	if requested == "" {
		requested = defaultRequestedVersion
	}

	resolvedVersion, err := resolveVersion(ctx, client, cacheDir, indexURLs, requested)
	if err != nil {
		return Result{}, err
	}

	cacheBinaryPath := binaryCachePath(cacheDir, resolvedVersion, goos, goarch)
	binaryDigest := ""
	usedCache := false
	if _, err := os.Stat(cacheBinaryPath); err == nil {
		binaryDigest, err = installutil.FileSHA256(cacheBinaryPath)
		if err != nil {
			return Result{}, err
		}
		usedCache = true
	} else {
		archiveChecksum, err := loadOrFetchArchiveChecksum(ctx, client, cacheDir, downloadBaseURLs, resolvedVersion, goos, goarch, archiveExt)
		if err != nil {
			return Result{}, err
		}
		archivePath := archiveCachePath(cacheDir, resolvedVersion, goos, goarch, archiveExt)
		if ok, err := installutil.FileMatchesChecksum(archivePath, archiveChecksum); err != nil {
			return Result{}, err
		} else if !ok {
			if _, err := installutil.DownloadToFile(ctx, client, archiveURLs(downloadBaseURLs, resolvedVersion, goos, goarch, archiveExt), archivePath, archiveChecksum, 0o644); err != nil {
				return Result{}, err
			}
		}
		if err := extractBinaryToCache(archivePath, cacheBinaryPath, goos); err != nil {
			return Result{}, err
		}
		binaryDigest, err = installutil.FileSHA256(cacheBinaryPath)
		if err != nil {
			return Result{}, err
		}
	}

	primarySourceURL := archiveURLs(downloadBaseURLs, resolvedVersion, goos, goarch, archiveExt)[0]
	if ok, err := installutil.FileMatchesChecksum(targetPath, binaryDigest); err == nil && ok {
		return Result{
			RequestedVersion: requested,
			ResolvedVersion:  resolvedVersion,
			BinaryPath:       targetPath,
			SHA256:           binaryDigest,
			SourceURL:        primarySourceURL,
			UsedCache:        true,
		}, nil
	}

	if err := installutil.CopyFileAtomic(cacheBinaryPath, targetPath, 0o755); err != nil {
		return Result{}, err
	}
	if ok, err := installutil.FileMatchesChecksum(targetPath, binaryDigest); err != nil || !ok {
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("installed binary checksum mismatch at %s", targetPath)
	}

	return Result{
		RequestedVersion: requested,
		ResolvedVersion:  resolvedVersion,
		BinaryPath:       targetPath,
		SHA256:           binaryDigest,
		SourceURL:        primarySourceURL,
		UsedCache:        usedCache,
	}, nil
}

func resolveVersion(ctx context.Context, client *http.Client, cacheDir string, indexURLs []string, requested string) (string, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" || strings.EqualFold(trimmed, "latest") || strings.EqualFold(trimmed, "stable") {
		version, err := loadOrFetchLatestVersion(ctx, client, cacheDir, indexURLs)
		if err != nil || version == "" {
			return defaultStableVersion, nil
		}
		return normalizeExactVersion(version), nil
	}

	if !versionPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid version format %q; expected latest or major.minor.patch", requested)
	}
	return normalizeExactVersion(trimmed), nil
}

func loadOrFetchLatestVersion(ctx context.Context, client *http.Client, cacheDir string, indexURLs []string) (string, error) {
	cachePath := filepath.Join(cacheDir, "versions", "latest.txt")
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, indexURLs)
	if err != nil {
		if value, cacheErr := installutil.ReadTrimmedFile(cachePath); cacheErr == nil && value != "" {
			return value, nil
		}
		return "", err
	}

	version, err := latestVersionFromIndex(data)
	if err != nil {
		return "", err
	}
	if err := installutil.WriteAtomic(cachePath, []byte(version+"\n"), 0o644); err != nil {
		return "", err
	}
	return version, nil
}

func loadOrFetchArchiveChecksum(ctx context.Context, client *http.Client, cacheDir string, downloadBaseURLs []string, version, goos, goarch, archiveExt string) (string, error) {
	cachePath := filepath.Join(cacheDir, "checksums", version, goos, goarch, checksumFileName(version))
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, checksumURLs(downloadBaseURLs, version))
	if err != nil {
		return "", err
	}
	checksum, err := checksumForArchive(string(data), archiveFileName(version, goos, goarch, archiveExt))
	if err != nil {
		return "", err
	}
	if err := installutil.WriteAtomic(cachePath, []byte(checksum+"\n"), 0o644); err != nil {
		return "", err
	}
	return checksum, nil
}

func extractBinaryToCache(archivePath, cacheBinaryPath, goos string) error {
	workDir := filepath.Join(filepath.Dir(cacheBinaryPath), "extract")
	if err := os.RemoveAll(workDir); err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	if err := installutil.ExtractZip(archivePath, workDir); err != nil {
		return err
	}

	binaryName := installutil.ExecutableName(defaultToolName, goos)
	binaryPath, err := installutil.FindFirstFile(workDir, binaryName)
	if err != nil {
		return err
	}
	return installutil.CopyFileAtomic(binaryPath, cacheBinaryPath, 0o755)
}

func archiveURLs(downloadBaseURLs []string, version, goos, goarch, archiveExt string) []string {
	return installutil.JoinURLPaths(downloadBaseURLs, filepath.ToSlash(filepath.Join(version, archiveFileName(version, goos, goarch, archiveExt))))
}

func checksumURLs(downloadBaseURLs []string, version string) []string {
	return installutil.JoinURLPaths(downloadBaseURLs, filepath.ToSlash(filepath.Join(version, checksumFileName(version))))
}

func archiveFileName(version, goos, goarch, archiveExt string) string {
	return fmt.Sprintf("terraform_%s_%s_%s.%s", version, goos, goarch, archiveExt)
}

func checksumFileName(version string) string {
	return fmt.Sprintf("terraform_%s_SHA256SUMS", version)
}

func archiveCachePath(cacheDir, version, goos, goarch, archiveExt string) string {
	return filepath.Join(cacheDir, "archives", version, goos, goarch, archiveFileName(version, goos, goarch, archiveExt))
}

func binaryCachePath(cacheDir, version, goos, goarch string) string {
	return filepath.Join(cacheDir, "binaries", version, goos, goarch, installutil.ExecutableName(defaultToolName, goos))
}

func checksumForArchive(data, archiveName string) (string, error) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == archiveName {
			return strings.TrimSpace(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found", archiveName)
}

func terraformPlatform(goos, goarch string) (string, string, string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64", "arm64", "arm":
			return goos, goarch, "zip", nil
		}
	case "darwin":
		switch goarch {
		case "amd64", "arm64":
			return goos, goarch, "zip", nil
		}
	case "windows":
		switch goarch {
		case "amd64":
			return goos, goarch, "zip", nil
		}
	}
	return "", "", "", fmt.Errorf("unsupported platform %q/%q", goos, goarch)
}

func normalizeExactVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func latestVersionFromIndex(data []byte) (string, error) {
	var payload releaseIndexPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode Terraform release index: %w", err)
	}
	if len(payload.Versions) == 0 {
		return "", fmt.Errorf("terraform release index did not include any versions")
	}

	versions := make([]string, 0, len(payload.Versions))
	for version := range payload.Versions {
		if versionPattern.MatchString(version) {
			versions = append(versions, normalizeExactVersion(version))
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("terraform release index did not include any stable versions")
	}

	sort.Slice(versions, func(left, right int) bool {
		return compareVersions(versions[left], versions[right]) < 0
	})
	return versions[len(versions)-1], nil
}

func compareVersions(left, right string) int {
	leftParts := versionParts(left)
	rightParts := versionParts(right)
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		if leftParts[index] < rightParts[index] {
			return -1
		}
		if leftParts[index] > rightParts[index] {
			return 1
		}
	}
	return 0
}

func versionParts(version string) [3]int {
	parts := strings.Split(normalizeExactVersion(version), ".")
	result := [3]int{}
	for index := 0; index < len(parts) && index < len(result); index++ {
		value, _ := strconv.Atoi(parts[index])
		result[index] = value
	}
	return result
}

func zipArchive(binaryName string, binary []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create(binaryName)
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(binary); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
