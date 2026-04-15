package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourceplane/tinx-providers/internal/installutil"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	if got := normalizeExactVersion("4.1.4"); got != "v4.1.4" {
		t.Fatalf("normalizeExactVersion() = %q", got)
	}
	if _, err := resolveVersion(context.Background(), http.DefaultClient, t.TempDir(), []string{defaultBaseURL}, "4.1"); err == nil {
		t.Fatal("expected invalid version error")
	}
}

func TestInstallUsesCachedBinary(t *testing.T) {
	goos, goarch, archiveExt, err := helmPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported runtime for test: %v", err)
	}

	version := "v4.1.4"
	archive := mustHelmArchive(t, goos, []byte("helm-test-binary"))
	archiveDigest := sha256.Sum256(archive)
	archiveChecksum := hex.EncodeToString(archiveDigest[:])

	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/helm-latest-version":
			_, _ = writer.Write([]byte(version))
		case "/" + archiveFileName(version, goos, goarch, archiveExt):
			_, _ = writer.Write(archive)
		case "/" + checksumFileName(version, goos, goarch, archiveExt):
			_, _ = writer.Write([]byte(archiveChecksum + "\n"))
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
		Mirrors:          []string{server.URL},
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	if firstResult.ResolvedVersion != version {
		t.Fatalf("resolved version = %q, want %q", firstResult.ResolvedVersion, version)
	}

	secondInstallDir := t.TempDir()
	secondResult, err := installer.Install(ctx, Config{
		RequestedVersion: version,
		InstallDir:       secondInstallDir,
		CacheDir:         cacheDir,
		Mirrors:          []string{"https://127.0.0.1:1"},
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
		t.Fatalf("installed binary checksum mismatch, ok=%t err=%v", ok, err)
	}
}

func mustHelmArchive(t *testing.T, goos string, binary []byte) []byte {
	t.Helper()
	if goos == "windows" {
		t.Fatal("windows runtime is not covered by this archive helper")
	}

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)

	entries := []struct {
		name string
		data []byte
		mode int64
	}{
		{name: fmt.Sprintf("%s-%s-%s/README.md", defaultToolName, goos, runtime.GOARCH), data: []byte("test"), mode: 0o644},
		{name: fmt.Sprintf("%s-%s-%s/%s", defaultToolName, goos, runtime.GOARCH, defaultToolName), data: binary, mode: 0o755},
	}

	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader() error = %v", err)
		}
		if _, err := tarWriter.Write(entry.data); err != nil {
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
