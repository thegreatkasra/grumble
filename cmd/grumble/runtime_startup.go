package main

import (
	"errors"
	"os"
	"path/filepath"
)

func runtimeCertificatePaths(dataDir string) (certPath, keyPath string) {
	return filepath.Join(dataDir, "cert.pem"), filepath.Join(dataDir, "key.pem")
}

func shouldManageRuntimeCertificate(cfg RuntimeConfig) bool {
	return !cfg.TeamlancerMode || cfg.EnableRawMumbleTCP
}

func ensureRuntimeCertificate(dataDir string, regen bool) (string, error) {
	certPath, keyPath := runtimeCertificatePaths(dataDir)
	shouldGenerate := regen

	if !shouldGenerate {
		hasCert := fileExists(certPath)
		hasKey := fileExists(keyPath)

		switch {
		case hasCert && hasKey:
			return "reused", nil
		case !hasCert && !hasKey:
			shouldGenerate = true
		case !hasCert:
			return "", errors.New("Grumble could not find its default certificate (cert.pem)")
		default:
			return "", errors.New("Grumble could not find its default private key (key.pem)")
		}
	}

	if err := GenerateSelfSignedCert(certPath, keyPath); err != nil {
		return "", err
	}
	return "generated", nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
