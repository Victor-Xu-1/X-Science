package server

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	managedExecutionOutputOwnershipMarker       = ".x-science-execution-pack.json"
	legacyManagedExecutionOutputOwnershipMarker = ".synon-execution-pack.json"
	managedExecutionOutputOwnershipSchema       = "x-science.execution-pack-output-owner.v1"
	legacyManagedExecutionOutputOwnershipSchema = "synon.execution-pack-output-owner.v1"
)

func isManagedExecutionOutputOwnershipMarkerBase(name string) bool {
	return name == managedExecutionOutputOwnershipMarker || name == legacyManagedExecutionOutputOwnershipMarker
}

func managedExecutionOutputOwnershipMarkerPaths(directory string) []string {
	paths := make([]string, 0, 2)
	for _, marker := range []string{managedExecutionOutputOwnershipMarker, legacyManagedExecutionOutputOwnershipMarker} {
		path := filepath.Join(directory, marker)
		if _, err := os.Lstat(path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

func readManagedExecutionOutputOwnershipMarker(path string) (string, managedExecutionOutputOwnership) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", managedExecutionOutputOwnershipMissing
	}
	if err != nil || len(raw) == 0 || len(raw) > 4096 {
		return "", managedExecutionOutputOwnershipInvalid
	}
	var marker struct {
		Schema          string `json:"schema"`
		ExecutionPackID string `json:"execution_pack_id"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return "", managedExecutionOutputOwnershipInvalid
	}
	if !acceptedManagedExecutionOutputOwnershipSchema(marker.Schema) || strings.TrimSpace(marker.ExecutionPackID) == "" {
		return "", managedExecutionOutputOwnershipInvalid
	}
	return strings.TrimSpace(marker.ExecutionPackID), managedExecutionOutputOwnershipValid
}

func acceptedManagedExecutionOutputOwnershipSchema(schema string) bool {
	return schema == managedExecutionOutputOwnershipSchema || schema == legacyManagedExecutionOutputOwnershipSchema
}

func inspectManagedExecutionOutputOwnership(directory string) (string, managedExecutionOutputOwnership) {
	for _, path := range managedExecutionOutputOwnershipMarkerPaths(directory) {
		packID, status := readManagedExecutionOutputOwnershipMarker(path)
		if status == managedExecutionOutputOwnershipValid {
			return packID, status
		}
		if status == managedExecutionOutputOwnershipInvalid {
			return "", status
		}
	}
	return "", managedExecutionOutputOwnershipMissing
}

func managedExecutionOutputOwnershipMarkerPresent(writes map[string]string, outputRoot string) bool {
	for _, marker := range []string{managedExecutionOutputOwnershipMarker, legacyManagedExecutionOutputOwnershipMarker} {
		if writes[filepath.Join(outputRoot, marker)] != "" {
			return true
		}
	}
	return false
}
