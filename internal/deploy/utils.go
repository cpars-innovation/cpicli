package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/manifest"
)

// FileExists checks if a file exists
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return err == nil && !info.IsDir()
}

// DirExists checks if a directory exists
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ValidateDeploymentPrefix validates that the deployment prefix only contains allowed characters
func ValidateDeploymentPrefix(prefix string) error {
	if prefix == "" {
		return nil // Empty prefix is valid
	}

	// Only allow alphanumeric and underscores
	matched, err := regexp.MatchString("^[a-zA-Z0-9_]+$", prefix)
	if err != nil {
		return fmt.Errorf("regex error: %w", err)
	}

	if !matched {
		return fmt.Errorf("deployment prefix can only contain alphanumeric characters (a-z, A-Z, 0-9) and underscores (_)")
	}

	return nil
}

// CopyDir recursively copies a directory
func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		// Copy file
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, info.Mode())
	})
}

// UpdateManifestBundleName sets Bundle-SymbolicName and Bundle-Name in
// MANIFEST.MF and writes the result to outputPath. Attributes of the symbolic
// name (e.g. "; singleton:=true") and all other headers (Bundle-Version, ...)
// are kept; values are wrapped at 72 bytes and continuation lines of the old
// values are removed.
func UpdateManifestBundleName(manifestPath, bundleSymbolicName, bundleName, outputPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read MANIFEST.MF: %w", err)
	}
	symbolic := bundleSymbolicName
	if _, attrs, ok := strings.Cut(manifest.Parse(data)["Bundle-SymbolicName"], ";"); ok && !strings.Contains(bundleSymbolicName, ";") {
		symbolic += ";" + attrs
	}
	out := manifest.SetHeaders(data, map[string]string{"Bundle-SymbolicName": symbolic, "Bundle-Name": bundleName})

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	if err := os.WriteFile(outputPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write MANIFEST.MF: %w", err)
	}
	return nil
}

// MergeParametersFile reads parameters.prop, applies overrides, and writes to outputPath
func MergeParametersFile(paramsPath string, overrides map[string]any, outputPath string) error {
	var lineEnding string = "\n"
	params := make(map[string]string)
	paramKeys := []string{} // Track order of keys

	// Read existing file if it exists
	if FileExists(paramsPath) {
		data, err := os.ReadFile(paramsPath)
		if err != nil {
			return fmt.Errorf("failed to read parameters.prop: %w", err)
		}

		// Detect line ending style
		content := string(data)
		if strings.Contains(content, "\r\n") {
			lineEnding = "\r\n"
		}

		// Split and process lines
		lines := strings.SplitSeq(content, lineEnding)

		for line := range lines {
			trimmed := strings.TrimSpace(line)

			// Keep comments and empty lines as-is
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}

			// Parse key=value
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				params[key] = value
				paramKeys = append(paramKeys, key)
			}
		}
	}

	// Apply overrides
	for key, value := range overrides {
		valStr := fmt.Sprintf("%v", value)
		if _, exists := params[key]; !exists {
			// New key, add to order
			paramKeys = append(paramKeys, key)
		}
		params[key] = valStr
	}

	// Write back with preserved order
	var result []string
	for _, key := range paramKeys {
		result = append(result, fmt.Sprintf("%s=%s", key, params[key]))
	}

	// Join with original line endings and ensure final newline
	finalContent := strings.Join(result, lineEnding)
	if !strings.HasSuffix(finalContent, lineEnding) {
		finalContent += lineEnding
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := os.WriteFile(outputPath, []byte(finalContent), 0644); err != nil {
		return fmt.Errorf("failed to write parameters.prop: %w", err)
	}

	return nil
}

// FindParametersFile finds parameters.prop in various possible locations
func FindParametersFile(artifactDir string) string {
	possiblePaths := []string{
		filepath.Join(artifactDir, "src", "main", "resources", "parameters.prop"),
		filepath.Join(artifactDir, "src", "main", "resources", "script", "parameters.prop"),
		filepath.Join(artifactDir, "parameters.prop"),
	}

	for _, path := range possiblePaths {
		if FileExists(path) {
			return path
		}
	}

	// Return default path even if it doesn't exist
	return possiblePaths[0]
}
