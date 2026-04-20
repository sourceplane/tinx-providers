package provider

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
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

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	if got := normalizeExactVersion("v565.0.0"); got != "565.0.0" {
		t.Fatalf("normalizeExactVersion() = %q", got)
	}
	if _, err := resolveVersion(context.Background(), http.DefaultClient, t.TempDir(), []string{defaultComponentsURL}, "565.0"); err == nil {
		t.Fatal("expected invalid version error")
	}
}

func TestInstallUsesCachedArchive(t *testing.T) {
	archiveSuffix, archiveExt, wrapperName, err := gcloudPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported runtime for test: %v", err)
	}

	version := "565.0.0"
	archiveName := archiveFileName(version, archiveSuffix, archiveExt)
	archive := mustGCloudArchive(t, runtime.GOOS)
	archiveMD5 := md5.Sum(archive)
	archiveMD5Base64 := base64.StdEncoding.EncodeToString(archiveMD5[:])

	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/downloads/" + archiveName:
			_, _ = writer.Write(archive)
		case "/metadata/" + archiveName:
			_ = json.NewEncoder(writer).Encode(objectMetadataPayload{Name: archiveName, MD5Hash: archiveMD5Base64})
		default:
			http.NotFound(writer, request)
		}
	}))
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
		DownloadMirrors:  []string{server.URL + "/downloads"},
		MetadataBaseURLs: []string{server.URL + "/metadata"},
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	if firstResult.ResolvedVersion != version {
		t.Fatalf("resolved version = %q, want %q", firstResult.ResolvedVersion, version)
	}
	if _, err := os.Stat(filepath.Join(firstInstallDir, installedSDKDirName, "bin", wrapperName)); err != nil {
		t.Fatalf("expected extracted SDK binary: %v", err)
	}

	secondInstallDir := t.TempDir()
	secondResult, err := installer.Install(ctx, Config{
		RequestedVersion: version,
		InstallDir:       secondInstallDir,
		CacheDir:         cacheDir,
		DownloadMirrors:  []string{"https://127.0.0.1:1"},
		MetadataBaseURLs: []string{"https://127.0.0.1:1"},
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
		t.Fatalf("installed wrapper checksum mismatch, ok=%t err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(secondInstallDir, installedSDKDirName, "bin", wrapperName)); err != nil {
		t.Fatalf("expected extracted SDK binary in second install: %v", err)
	}
}

func mustGCloudArchive(t *testing.T, goos string) []byte {
	t.Helper()
	binaryName := defaultToolName
	if goos == "windows" {
		binaryName = defaultToolName + ".cmd"
		return mustZipArchive(t, map[string][]byte{
			installedSDKDirName + "/bin/" + binaryName: []byte("@echo off\r\necho gcloud\r\n"),
		})
	}
	return mustTarGzArchive(t, map[string][]byte{
		installedSDKDirName + "/bin/" + binaryName: []byte("#!/bin/sh\necho gcloud\n"),
	})
}

func mustTarGzArchive(t *testing.T, files map[string][]byte) []byte {
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

func mustZipArchive(t *testing.T, files map[string][]byte) []byte {
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
