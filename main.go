// main.go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Configuration structures
type Config struct {
	Sonarr     SonarrConfig    `json:"sonarr"`
	Radarr     RadarrConfig    `json:"radarr"`
	Transforms []Transform     `json:"transforms"`
}

type SonarrConfig struct {
	URL             string `json:"url"`
	APIKey          string `json:"apikey"`
	DownloadsFolder string `json:"downloadsFolder"`
	MappingPath     string `json:"mappingPath"`
}

type RadarrConfig struct {
	URL             string `json:"url"`
	APIKey          string `json:"apikey"`
	DownloadsFolder string `json:"downloadsFolder"`
	MappingPath     string `json:"mappingPath"`
}

type Transform struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`
}

// Sonarr API structures
type ImportRequest struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	ImportMode   string `json:"importMode"`
	Quality      map[string]interface{} `json:"quality,omitempty"`
}

type ImportResponse struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

// Global configuration
var (
	config     Config
	httpClient = &http.Client{Timeout: 30 * time.Second}
	verbose    bool
	dryRun     bool
)

// Video file extensions
var videoExtensions = map[string]bool{
	".mp4":  true,
	".mkv":  true,
	".avi":  true,
	".m4v":  true,
	".mov":  true,
	".wmv":  true,
	".flv":  true,
	".webm": true,
	".ts":   true,
	".m2ts": true,
}

func main() {
	// Command line flags
	var configPath string
	flag.StringVar(&configPath, "c", "Settings.json", "Path to configuration file")
	flag.BoolVar(&verbose, "v", false, "Verbose logging")
	flag.BoolVar(&dryRun, "dry-run", false, "Dry run mode - don't actually import")
	flag.Parse()

	// Load configuration
	if err := loadConfig(configPath); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logInfo("SonarrAutoImport Go Edition")
	logInfo("===========================")
	logInfo(fmt.Sprintf("Config: %s", configPath))
	logInfo(fmt.Sprintf("Dry run: %t", dryRun))
	logInfo(fmt.Sprintf("Verbose: %t", verbose))
	logInfo("")

	// Check if running in daemon mode (environment variable)
	if os.Getenv("DAEMON_MODE") == "true" {
		runDaemon()
	} else {
		// Single run
		if err := scanAndImport(); err != nil {
			log.Fatalf("Scan failed: %v", err)
		}
	}
}

func runDaemon() {
	interval := 5 * time.Minute // Default interval
	if envInterval := os.Getenv("SCAN_INTERVAL"); envInterval != "" {
		if duration, err := time.ParseDuration(envInterval + "s"); err == nil {
			interval = duration
		}
	}

	logInfo(fmt.Sprintf("Running in daemon mode, scanning every %v", interval))

	// Initial scan
	if err := scanAndImport(); err != nil {
		logError(fmt.Sprintf("Initial scan failed: %v", err))
	}

	// Periodic scanning
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		logInfo("Starting scheduled scan...")
		if err := scanAndImport(); err != nil {
			logError(fmt.Sprintf("Scheduled scan failed: %v", err))
		}
	}
}

func loadConfig(path string) error {
	// Check if config file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create default config
		logInfo("Creating default configuration file...")
		return createDefaultConfig(path)
	}

	// Load existing config
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Replace environment variables in config
	configStr := os.ExpandEnv(string(data))

	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return fmt.Errorf("failed to parse config JSON: %w", err)
	}

	return nil
}

func createDefaultConfig(path string) error {
	defaultConfig := Config{
		Sonarr: SonarrConfig{
			URL:             "${SONARR_URL:-http://sonarr:8989}",
			APIKey:          "${SONARR_API_KEY}",
			DownloadsFolder: "/media",
			MappingPath:     "/downloads",
		},
		Radarr: RadarrConfig{
			URL:             "${RADARR_URL:-}",
			APIKey:          "${RADARR_API_KEY:-}",
			DownloadsFolder: "/media",
			MappingPath:     "/downloads",
		},
		Transforms: []Transform{
			{Search: `Series (\d+) - `, Replace: "S$1E"},
			{Search: `Season (\d+) Episode (\d+)`, Replace: "S$1E$2"},
			{Search: `\.(\d{4})\.`, Replace: ".$1."},
			{Search: `^(.+?) - (\d{4})\.(\d{2})\.(\d{2}) - `, Replace: "$1 $2-$3-$4 "},
		},
	}

	data, err := json.MarshalIndent(defaultConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	logInfo(fmt.Sprintf("Created default config at %s", path))
	logInfo("Please edit the configuration file and restart.")
	return nil
}

func scanAndImport() error {
	// Validate configuration
	if config.Sonarr.URL == "" || config.Sonarr.APIKey == "" {
		return fmt.Errorf("Sonarr URL and API key are required")
	}

	// Check if downloads folder exists
	if _, err := os.Stat(config.Sonarr.DownloadsFolder); os.IsNotExist(err) {
		return fmt.Errorf("downloads folder not found: %s", config.Sonarr.DownloadsFolder)
	}

	// Find video files
	videoFiles, err := findVideoFiles(config.Sonarr.DownloadsFolder)
	if err != nil {
		return fmt.Errorf("failed to scan for video files: %w", err)
	}

	logInfo(fmt.Sprintf("Found %d video files in %s", len(videoFiles), config.Sonarr.DownloadsFolder))

	if len(videoFiles) == 0 {
		logInfo("No video files to process")
		return nil
	}

	// Process each file
	processed := 0
	for _, file := range videoFiles {
		if err := processVideoFile(file); err != nil {
			logError(fmt.Sprintf("Failed to process %s: %v", filepath.Base(file), err))
			continue
		}
		processed++
	}

	logInfo(fmt.Sprintf("Processing complete. %d/%d files processed successfully", processed, len(videoFiles)))
	return nil
}

func findVideoFiles(rootPath string) ([]string, error) {
	var videoFiles []string

	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if videoExtensions[ext] {
			videoFiles = append(videoFiles, path)
		}

		return nil
	})

	return videoFiles, err
}

func processVideoFile(filePath string) error {
	fileName := filepath.Base(filePath)
	logVerbose(fmt.Sprintf("Processing file: %s", fileName))

	// Apply filename transforms
	transformedName := applyTransforms(fileName)
	if transformedName != fileName {
		logVerbose(fmt.Sprintf("  Transformed: %s → %s", fileName, transformedName))
	}

	// Convert local path to Sonarr mapping path
	mappedPath := convertToMappingPath(filePath)
	logVerbose(fmt.Sprintf("  Mapped path: %s", mappedPath))

	if dryRun {
		logInfo(fmt.Sprintf("  [DRY RUN] Would import: %s", transformedName))
		return nil
	}

	// Import to Sonarr
	return importToSonarr(mappedPath, transformedName)
}

func applyTransforms(fileName string) string {
	result := fileName

	for _, transform := range config.Transforms {
		regex, err := regexp.Compile(transform.Search)
		if err != nil {
			logError(fmt.Sprintf("Invalid regex pattern: %s", transform.Search))
			continue
		}

		newResult := regex.ReplaceAllString(result, transform.Replace)
		if newResult != result {
			logVerbose(fmt.Sprintf("  Transform applied: %s → %s", result, newResult))
			result = newResult
		}
	}

	return result
}

func convertToMappingPath(localPath string) string {
	// Replace the local downloads folder with the mapping path
	rel, err := filepath.Rel(config.Sonarr.DownloadsFolder, localPath)
	if err != nil {
		return localPath
	}

	return filepath.Join(config.Sonarr.MappingPath, rel)
}

func importToSonarr(filePath, fileName string) error {
	// Sonarr Import API endpoint
	url := fmt.Sprintf("%s/api/v3/command", strings.TrimRight(config.Sonarr.URL, "/"))

	// Command to trigger import
	command := map[string]interface{}{
		"name": "DownloadedEpisodesScan",
		"path": filepath.Dir(filePath),
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("failed to marshal import request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", config.Sonarr.APIKey)

	logVerbose(fmt.Sprintf("  Sending import request to: %s", url))

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send import request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("import request failed with status: %d", resp.StatusCode)
	}

	logInfo(fmt.Sprintf("  ✓ Import triggered: %s", fileName))
	return nil
}

func logInfo(message string) {
	log.Printf("[INFO] %s", message)
}

func logError(message string) {
	log.Printf("[ERROR] %s", message)
}

func logVerbose(message string) {
	if verbose {
		log.Printf("[VERBOSE] %s", message)
	}
}
