package controllers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
)

// sdkManifest mirrors the subset of the SDK package manifest schema
// driven by the upload logic.
type sdkManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// loadSDKManifest reads the embedded manifest.json.
func loadSDKManifest(sdkFS fs.FS) (sdkManifest, error) {
	var m sdkManifest
	data, err := fs.ReadFile(sdkFS, "sdk/manifest.json")
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse embedded manifest.json: %w", err)
	}
	return m, nil
}

// patchManifest updates the manifest.json content with priorities from config.
func patchManifest(data []byte) ([]byte, error) {
	var mf map[string]interface{}
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, err
	}

	pc, ok := mf["profile_cards"].([]interface{})
	if !ok {
		return data, nil
	}

	priorities := config.AppConfig.Microservice.ProfileCards
	if len(priorities) == 0 {
		return data, nil
	}

	changed := false
	for i := range pc {
		card, ok := pc[i].(map[string]interface{})
		if !ok {
			continue
		}
		title, _ := card["title"].(string)
		if priority, exists := priorities[title]; exists {
			card["priority"] = priority
			changed = true
		}
	}

	if !changed {
		return data, nil
	}

	return json.MarshalIndent(mf, "", "  ")
}

// buildSDKArchive packs the embedded sdk/ source tree into an in-memory
// tar.gz archive with manifest.json at its root.
func buildSDKArchive(sdkFS fs.FS) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// embed.FS paths start with "sdk/"; strip that prefix so the archive
	// carries manifest.json at the root and src/... with relative paths.
	if err := fs.WalkDir(sdkFS, "sdk", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(sdkFS, p)
		if err != nil {
			return err
		}

		name := strings.TrimPrefix(p, "sdk/")
		if name == "manifest.json" {
			patched, err := patchManifest(data)
			if err == nil {
				data = patched
			} else {
				log.Printf("Warning: Failed to patch SDK manifest: %v", err)
			}
		}

		hdr := &tar.Header{
			Name:    name,
			Mode:    0644,
			Size:    int64(len(data)),
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	}); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UploadSDKPackage uploads the compile-time SDK package to HRPAuth
// asynchronously. If a package with the same name already exists (409),
// the stale package is deleted and the upload retried once.
func (sc *StartupController) UploadSDKPackage(sdkFS fs.FS) {
	go func() {
		manifest, err := loadSDKManifest(sdkFS)
		if err != nil {
			log.Printf("Warning: Load embedded sdk manifest failed: %v", err)
			return
		}
		archive, err := buildSDKArchive(sdkFS)
		if err != nil {
			log.Printf("Warning: Build sdk archive failed: %v", err)
			return
		}

		coreClient := clients.NewCoreClient()
		filename := manifest.Name + "-sdk.tar.gz"
		if err := coreClient.UploadSDKPackage(archive, filename); err != nil {
			if !errors.Is(err, clients.ErrSDKPackageConflict) {
				log.Printf("Warning: SDK package upload to HRPAuth failed: %v", err)
				return
			}
			log.Printf("SDK package %q already exists on HRPAuth, deleting and re-uploading", manifest.Name)
			if delErr := coreClient.DeleteSDKPackage(manifest.Name); delErr != nil {
				log.Printf("Warning: Delete stale sdk package failed: %v", delErr)
				return
			}
			if err := coreClient.UploadSDKPackage(archive, filename); err != nil {
				log.Printf("Warning: SDK package re-upload after delete failed: %v", err)
				return
			}
		}
		log.Printf("SDK package %q (v%s) uploaded to HRPAuth", manifest.Name, manifest.Version)
	}()
}
