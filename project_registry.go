package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const projectFolderRegistryVersion = 1

type projectFolderRegistry struct {
	Version int      `json:"version"`
	Folders []string `json:"folders"`
}

func defaultProjectFolderRegistryPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config folder: %w", err)
	}
	if strings.TrimSpace(configDir) == "" {
		return "", errors.New("user config folder is empty")
	}
	return filepath.Join(configDir, "Nala Ctrl", "project-folders.json"), nil
}

func readProjectFolderRegistry(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var registry projectFolderRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("decode saved project list: %w", err)
	}
	if registry.Version != projectFolderRegistryVersion {
		return nil, fmt.Errorf("unsupported saved project list version %d", registry.Version)
	}
	if registry.Folders == nil {
		registry.Folders = []string{}
	}
	return registry.Folders, nil
}

func writeProjectFolderRegistry(path string, folders []string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("saved project list path is empty")
	}
	data, err := json.MarshalIndent(projectFolderRegistry{
		Version: projectFolderRegistryVersion,
		Folders: folders,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode saved project list: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create saved project list folder: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".project-folders-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary saved project list: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set saved project list permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write saved project list: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync saved project list: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close saved project list: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace saved project list: %w", err)
	}
	return nil
}

func appendProjectFolder(folders []string, folder string) ([]string, bool) {
	folder = filepath.Clean(folder)
	identity := projectIDForFolder(folder)
	for _, existing := range folders {
		if filepath.IsAbs(existing) && projectIDForFolder(existing) == identity {
			return folders, false
		}
	}
	return append(append([]string(nil), folders...), folder), true
}
