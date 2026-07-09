package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyDataDirWritableWithMissingDirectory(t *testing.T) {
	cfg := RuntimeConfig{DataDir: filepath.Join(t.TempDir(), "missing", "nested")}
	if err := cfg.VerifyDataDirWritable(); err != nil {
		t.Fatalf("expected missing directory to be created and writable: %v", err)
	}
}

func TestVerifyDataDirWritableWithUnwritableDirectory(t *testing.T) {
	tempDir := t.TempDir()
	if runtime.GOOS == "windows" {
		filePath := filepath.Join(tempDir, "blocked")
		if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cfg := RuntimeConfig{DataDir: filepath.Join(filePath, "child")}
		if err := cfg.VerifyDataDirWritable(); err == nil {
			t.Fatal("expected file-backed path to be rejected as unwritable")
		}
		return
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permission checks")
	}

	cfg := RuntimeConfig{DataDir: filepath.Join(tempDir, "readonly")}
	if err := os.MkdirAll(cfg.DataDir, 0o500); err != nil {
		t.Fatalf("mkdir readonly: %v", err)
	}
	if err := os.Chmod(cfg.DataDir, 0o500); err != nil {
		t.Fatalf("chmod readonly: %v", err)
	}
	defer os.Chmod(cfg.DataDir, 0o700)

	if err := cfg.VerifyDataDirWritable(); err == nil {
		t.Fatal("expected readonly directory to fail writability check")
	}
}

func TestVerifyDataDirWritableWithWritableDirectory(t *testing.T) {
	cfg := RuntimeConfig{DataDir: filepath.Join(t.TempDir(), "writable")}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		t.Fatalf("mkdir writable: %v", err)
	}
	if err := cfg.VerifyDataDirWritable(); err != nil {
		t.Fatalf("expected writable directory to pass: %v", err)
	}
}

func TestEnsureRuntimeCertificateGeneratesMissingCertificate(t *testing.T) {
	tempDir := t.TempDir()
	state, err := ensureRuntimeCertificate(tempDir, false)
	if err != nil {
		t.Fatalf("expected certificate generation to succeed: %v", err)
	}
	if state != "generated" {
		t.Fatalf("expected generated state, got %q", state)
	}
	certPath, keyPath := runtimeCertificatePaths(tempDir)
	if !fileExists(certPath) || !fileExists(keyPath) {
		t.Fatal("expected generated certificate files to exist")
	}
}

func TestEnsureRuntimeCertificateReusesExistingCertificate(t *testing.T) {
	tempDir := t.TempDir()
	certPath, keyPath := runtimeCertificatePaths(tempDir)
	if err := GenerateSelfSignedCert(certPath, keyPath); err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	beforeCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert before: %v", err)
	}
	beforeKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key before: %v", err)
	}

	state, err := ensureRuntimeCertificate(tempDir, false)
	if err != nil {
		t.Fatalf("expected certificate reuse to succeed: %v", err)
	}
	if state != "reused" {
		t.Fatalf("expected reused state, got %q", state)
	}

	afterCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert after: %v", err)
	}
	afterKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key after: %v", err)
	}
	if string(beforeCert) != string(afterCert) || string(beforeKey) != string(afterKey) {
		t.Fatal("expected existing certificate and key to remain unchanged")
	}
}

func TestEnsureRuntimeCertificateSkipsGenerationWhenRawTCPDisabled(t *testing.T) {
	cfg := RuntimeConfig{
		TeamlancerMode:     true,
		EnableRawMumbleTCP: false,
	}
	if shouldManageRuntimeCertificate(cfg) {
		t.Fatal("expected raw tcp disabled Teamlancer config to skip certificate management")
	}
}
