package provider

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourceplane/kiox-providers/internal/installutil"
)

func TestResolveReleaseAsset(t *testing.T) {
	t.Parallel()

	if got := normalizeExactVersion("v2.85.0"); got != "2.85.0" {
		t.Fatalf("normalizeExactVersion() = %q", got)
	}
	if got := releaseVersionFromTag("azure-cli-2.85.0"); got != "2.85.0" {
		t.Fatalf("releaseVersionFromTag() = %q", got)
	}
}

func TestInstallUsesCachedArchive(t *testing.T) {
	assetSuffix, archiveExt, launcherName, err := azureCLIPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported runtime for test: %v", err)
	}

	version := "2.85.0"
	assetName := archiveFileName(version, assetSuffix, archiveExt)
	archive := mustAzureCLIArchive(t, runtime.GOOS)
	archiveDigest := sha256.Sum256(archive)
	archiveSHA256 := hex.EncodeToString(archiveDigest[:])

	var requests atomic.Int64
	serverURL := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/releases/tags/azure-cli-" + version:
			_ = json.NewEncoder(writer).Encode(releasePayload{
				TagName: "azure-cli-" + version,
				Assets: []releaseAsset{{
					Name:               assetName,
					Digest:             "sha256:" + archiveSHA256,
					BrowserDownloadURL: serverURL + "/downloads/" + assetName,
				}},
			})
		case "/downloads/" + assetName:
			_, _ = writer.Write(archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	cacheDir := t.TempDir()
	installer := NewInstaller()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	firstInstallDir := t.TempDir()
	firstResult, err := installer.Install(ctx, Config{
		RequestedVersion: version,
		InstallDir:       firstInstallDir,
		CacheDir:         cacheDir,
		ReleaseAPIURLs:   []string{server.URL + "/releases"},
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	if firstResult.ResolvedVersion != version {
		t.Fatalf("resolved version = %q, want %q", firstResult.ResolvedVersion, version)
	}
	if _, err := os.Stat(filepath.Join(firstInstallDir, "bin", launcherName)); err != nil {
		t.Fatalf("expected Azure CLI launcher: %v", err)
	}
	if runtime.GOOS != "windows" {
		launcherContent, err := os.ReadFile(filepath.Join(firstInstallDir, "bin", launcherName))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if !bytes.Contains(launcherContent, []byte("AZ_PYTHON")) {
			t.Fatal("expected Azure CLI wrapper to inject AZ_PYTHON")
		}
	}

	secondInstallDir := t.TempDir()
	secondResult, err := installer.Install(ctx, Config{
		RequestedVersion: version,
		InstallDir:       secondInstallDir,
		CacheDir:         cacheDir,
		ReleaseAPIURLs:   []string{"https://127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	if !secondResult.UsedCache {
		t.Fatal("expected second install to use cache")
	}
	if requests.Load() != 2 {
		t.Fatalf("network requests = %d, want 2", requests.Load())
	}
	if ok, err := installutil.FileMatchesChecksum(secondResult.BinaryPath, secondResult.SHA256); err != nil || !ok {
		t.Fatalf("installed launcher checksum mismatch, ok=%t err=%v", ok, err)
	}
}

func mustAzureCLIArchive(t *testing.T, goos string) []byte {
	t.Helper()
	if goos == "windows" {
		return mustAzureZipArchive(t, map[string][]byte{
			"bin/az.cmd": []byte("@echo off\r\necho az\r\n"),
		})
	}

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)

	regularFiles := map[string][]byte{
		"./libexec/bin/az": []byte("#!/usr/bin/env bash\nif [[ -z \"${AZ_PYTHON:-}\" ]]; then\n  echo missing python >&2\n  exit 1\nfi\necho az\n"),
		"./libexec/lib/python3.13/site-packages/azure/__init__.py": []byte(""),
	}
	for name, data := range regularFiles {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader() error = %v", err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	symlinkHeader := &tar.Header{
		Name:     "./bin/az",
		Mode:     0o755,
		Typeflag: tar.TypeSymlink,
		Linkname: "../libexec/bin/az",
	}
	if err := tarWriter.WriteHeader(symlinkHeader); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}

	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tarWriter.Close() error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzipWriter.Close() error = %v", err)
	}
	return buffer.Bytes()
}

func mustAzureTarGzArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, data := range files {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader() error = %v", err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tarWriter.Close() error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzipWriter.Close() error = %v", err)
	}
	return buffer.Bytes()
}

func mustAzureZipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	return buffer.Bytes()
}
