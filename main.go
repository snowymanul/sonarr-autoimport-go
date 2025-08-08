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
	"strconv"
	"strings"
	"time"
)

// Configuration structures
type Config struct {
	Sonarr     SonarrConfig    `json:"sonarr"`
	Parsing    ParsingConfig   `json:"parsing"`
	Transforms []Transform     `json:"transforms"`
}

type SonarrConfig struct {
	URL             string `json:"url"`
	APIKey          string `json:"apikey"`
	DownloadsFolder string `json:"downloadsFolder"`
	QualityProfile  int    `json:"qualityProfile"`
	LanguageProfile int    `json:"languageProfile"`
	RootFolder      string `json:"rootFolder"`
}

type ParsingConfig struct {
	AnimePatterns    []AnimePattern `json:"animePatterns"`
	SeasonPatterns   []string       `json:"seasonPatterns"`
	EpisodePatterns  []string       `json:"episodePatterns"`
	QualityPatterns  []string       `json:"qualityPatterns"`
	GroupPatterns    []string       `json:"groupPatterns"`
}

type AnimePattern struct {
	Pattern     string `json:"pattern"`
	TitleGroup  int    `json:"titleGroup"`
	SeasonGroup int    `json:"seasonGroup"`
	EpisodeGroup int   `json:"episodeGroup"`
}

type Transform struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`
}

// Sonarr API structures
type Series struct {
	ID                int    `json:"id"`
	Title             string `json:"title"`
	SortTitle         string `json:"sortTitle"`
	Status            string `json:"status"`
	Overview          string `json:"overview"`
	Network           string `json:"network"`
	AirTime           string `json:"airTime"`
	Images            []Image `json:"images"`
	Seasons           []Season `json:"seasons"`
	Year              int    `json:"year"`
	Path              string `json:"path"`
	QualityProfileID  int    `json:"qualityProfileId"`
	LanguageProfileID int    `json:"languageProfileId"`
	SeasonFolder      bool   `json:"seasonFolder"`
	Monitored         bool   `json:"monitored"`
	UseSceneNumbering bool   `json:"useSceneNumbering"`
	Runtime           int    `json:"runtime"`
	TvdbID            int    `json:"tvdbId"`
	TvRageID          int    `json:"tvRageId"`
	TvMazeID          int    `json:"tvMazeId"`
	FirstAired        string `json:"firstAired"`
	SeriesType        string `json:"seriesType"`
	CleanTitle        string `json:"cleanTitle"`
	ImdbID            string `json:"imdbId"`
	TitleSlug         string `json:"titleSlug"`
	RootFolderPath    string `json:"rootFolderPath"`
	Genres            []string `json:"genres"`
	Tags              []int    `json:"tags"`
	Added             string   `json:"added"`
	AddOptions        AddOptions `json:"addOptions"`
}

type Image struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
}

type Season struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

type AddOptions struct {
	IgnoreEpisodesWithFiles    bool `json:"ignoreEpisodesWithFiles"`
	IgnoreEpisodesWithoutFiles bool `json:"ignoreEpisodesWithoutFiles"`
	SearchForMissingEpisodes   bool `json:"searchForMissingEpisodes"`
}

type SeriesLookup struct {
	Title      string   `json:"title"`
	SortTitle  string   `json:"sortTitle"`
	Status     string   `json:"status"`
	Overview   string   `json:"overview"`
	Network    string   `json:"network"`
	Images     []Image  `json:"images"`
	Seasons    []Season `json:"seasons"`
	Year       int      `json:"year"`
	TvdbID     int      `json:"tvdbId"`
	TitleSlug  string   `json:"titleSlug"`
	Genres     []string `json:"genres"`
	FirstAired string   `json:"firstAired"`
}

type ParsedAnime struct {
	OriginalFilename string
	FilePath         string
	Title            string
	Season           int
	Episode          int
	Quality          string
	Group            string
	Year             int
}

type ManualImportRequest struct {
	Files []ManualImportFile `json:"files"`
}

type ManualImportFile struct {
	Path         string `json:"path"`
	SeriesID     int    `json:"seriesId"`
	SeasonNumber int    `json:"seasonNumber"`
	Episodes     []int  `json:"episodes"`
	Quality      Quality `json:"quality"`
	Language     Language `json:"language"`
}

type Quality struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Language struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Episode struct {
	ID           int    `json:"id"`
	SeriesID     int    `json:"seriesId"`
	EpisodeNumber int   `json:"episodeNumber"`
	SeasonNumber  int   `json:"seasonNumber"`
	Title         string `json:"title"`
	AirDate       string `json:"airDate"`
	Overview      string `json:"overview"`
	HasFile       bool   `json:"hasFile"`
	Monitored     bool   `json:"monitored"`
}

// Global configuration
var (
	config     Config
	httpClient = &http.Client{Timeout: 60 * time.Second}
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

	logInfo("SonarrAutoImport Go Edition - Anime Workflow")
	logInfo("=============================================")
	logInfo(fmt.Sprintf("Config: %s", configPath))
	logInfo(fmt.Sprintf("Dry run: %t", dryRun))
	logInfo(fmt.Sprintf("Verbose: %t", verbose))
	logInfo("")

	// Check if running in daemon mode (environment variable)
	if os.Getenv("DAEMON_MODE") == "true" {
		runDaemon()
	} else {
		// Single run
		if err := processAnimeFiles(); err != nil {
			log.Fatalf("Processing failed: %v", err)
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
	if err := processAnimeFiles(); err != nil {
		logError(fmt.Sprintf("Initial scan failed: %v", err))
	}

	// Periodic scanning
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		logInfo("Starting scheduled scan...")
		if err := processAnimeFiles(); err != nil {
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
			DownloadsFolder: "/downloads",
			QualityProfile:  1,
			LanguageProfile: 1,
			RootFolder:      "/tv",
		},
		Parsing: ParsingConfig{
			AnimePatterns: []AnimePattern{
				{
					Pattern:      `^(.+?)[\s_]+(\d+)(?:nd|rd|th)?[\s_]+Season[\s_]*\[(\d+)\]`,
					TitleGroup:   1,
					SeasonGroup:  2,
					EpisodeGroup: 3,
				},
				{
					Pattern:      `^(.+?)[\s_]+Season[\s_]+(\d+)[\s_]*\[(\d+)\]`,
					TitleGroup:   1,
					SeasonGroup:  2,
					EpisodeGroup: 3,
				},
				{
					Pattern:      `^(.+?)[\s_]*\[(\d+)\]`,
					TitleGroup:   1,
					SeasonGroup:  0,
					EpisodeGroup: 2,
				},
				{
					Pattern:      `^(.+?)[\s_]+S(\d+)E(\d+)`,
					TitleGroup:   1,
					SeasonGroup:  2,
					EpisodeGroup: 3,
				},
			},
			SeasonPatterns: []string{
				`(\d+)(?:nd|rd|th)?\s+Season`,
				`Season\s+(\d+)`,
				`S(\d+)`,
			},
			EpisodePatterns: []string{
				`\[(\d+)\]`,
				`E(\d+)`,
				`Episode\s+(\d+)`,
				`Ep\s*(\d+)`,
			},
			QualityPatterns: []string{
				`1080p`,
				`720p`,
				`480p`,
				`WEBRip`,
				`BluRay`,
				`DVDRip`,
			},
			GroupPatterns: []string{
				`\[([^\]]+)\]$`,
				`\(([^)]+)\)$`,
			},
		},
		Transforms: []Transform{
			{Search: `_`, Replace: ` `},
			{Search: `\.`, Replace: ` `},
			{Search: `\s+`, Replace: ` `},
			{Search: `^\s+|\s+$`, Replace: ``},
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
	logInfo("Please edit the configuration file with your Sonarr settings and restart.")
	return nil
}

func processAnimeFiles() error {
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

	// Parse and process each file
	processed := 0
	for _, file := range videoFiles {
		if err := processAnimeFile(file); err != nil {
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

func processAnimeFile(filePath string) error {
	fileName := filepath.Base(filePath)
	logVerbose(fmt.Sprintf("Processing file: %s", fileName))

	// Parse anime information from filename
	anime, err := parseAnimeFilename(fileName, filePath)
	if err != nil {
		return fmt.Errorf("failed to parse anime info: %w", err)
	}

	logInfo(fmt.Sprintf("Parsed: %s S%02dE%02d", anime.Title, anime.Season, anime.Episode))

	if dryRun {
		logInfo(fmt.Sprintf("[DRY RUN] Would process: %s", anime.Title))
		return nil
	}

	// Step 1: Find or create series in Sonarr
	seriesID, err := findOrCreateSeries(anime)
	if err != nil {
		return fmt.Errorf("failed to find/create series: %w", err)
	}

	// Step 2: Get episode information
	episodeID, err := findEpisode(seriesID, anime.Season, anime.Episode)
	if err != nil {
		return fmt.Errorf("failed to find episode: %w", err)
	}

	// Step 3: Import file using manual import
	if err := manualImport(anime, seriesID, episodeID); err != nil {
		return fmt.Errorf("failed to import file: %w", err)
	}

	logInfo(fmt.Sprintf("✓ Successfully imported: %s S%02dE%02d", anime.Title, anime.Season, anime.Episode))
	return nil
}

func parseAnimeFilename(filename, filepath string) (*ParsedAnime, error) {
	anime := &ParsedAnime{
		OriginalFilename: filename,
		FilePath:         filepath,
		Season:           1, // Default to season 1
	}

	// Remove file extension
	nameWithoutExt := strings.TrimSuffix(filename, filepath2.Ext(filename))

	// Apply transforms to clean up the filename
	cleanName := applyTransforms(nameWithoutExt)
	
	logVerbose(fmt.Sprintf("Cleaned filename: %s", cleanName))

	// Try each anime pattern
	for _, pattern := range config.Parsing.AnimePatterns {
		regex, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			logError(fmt.Sprintf("Invalid anime pattern: %s", pattern.Pattern))
			continue
		}

		matches := regex.FindStringSubmatch(cleanName)
		if len(matches) > pattern.TitleGroup {
			anime.Title = strings.TrimSpace(matches[pattern.TitleGroup])
			
			if pattern.SeasonGroup > 0 && len(matches) > pattern.SeasonGroup {
				if season, err := strconv.Atoi(matches[pattern.SeasonGroup]); err == nil {
					anime.Season = season
				}
			}
			
			if pattern.EpisodeGroup > 0 && len(matches) > pattern.EpisodeGroup {
				if episode, err := strconv.Atoi(matches[pattern.EpisodeGroup]); err == nil {
					anime.Episode = episode
				}
			}
			
			logVerbose(fmt.Sprintf("Pattern matched: %s -> Title: %s, Season: %d, Episode: %d", 
				pattern.Pattern, anime.Title, anime.Season, anime.Episode))
			break
		}
	}

	// If no pattern matched, try to extract title and episode manually
	if anime.Title == "" {
		anime.Title = extractTitle(cleanName)
		anime.Episode = extractEpisode(cleanName)
	}

	// Extract additional information
	anime.Quality = extractQuality(filename)
	anime.Group = extractGroup(filename)

	if anime.Title == "" || anime.Episode == 0 {
		return nil, fmt.Errorf("could not parse title or episode from filename")
	}

	return anime, nil
}

func applyTransforms(input string) string {
	result := input

	for _, transform := range config.Transforms {
		regex, err := regexp.Compile(transform.Search)
		if err != nil {
			logError(fmt.Sprintf("Invalid regex pattern: %s", transform.Search))
			continue
		}

		newResult := regex.ReplaceAllString(result, transform.Replace)
		if newResult != result {
			logVerbose(fmt.Sprintf("Transform applied: %s -> %s", result, newResult))
			result = newResult
		}
	}

	return result
}

func extractTitle(filename string) string {
	// Remove common patterns and extract title
	title := filename
	
	// Remove episode indicators
	patterns := []string{
		`\s*\[\d+\].*$`,
		`\s*[Ee]p?\s*\d+.*$`,
		`\s*[Ee]pisode\s*\d+.*$`,
		`\s*S\d+E\d+.*$`,
	}
	
	for _, pattern := range patterns {
		regex, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		title = regex.ReplaceAllString(title, "")
	}
	
	return strings.TrimSpace(title)
}

func extractEpisode(filename string) int {
	for _, pattern := range config.Parsing.EpisodePatterns {
		regex, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		
		matches := regex.FindStringSubmatch(filename)
		if len(matches) >= 2 {
			if episode, err := strconv.Atoi(matches[1]); err == nil {
				return episode
			}
		}
	}
	return 0
}

func extractQuality(filename string) string {
	for _, pattern := range config.Parsing.QualityPatterns {
		if strings.Contains(strings.ToLower(filename), strings.ToLower(pattern)) {
			return pattern
		}
	}
	return "Unknown"
}

func extractGroup(filename string) string {
	for _, pattern := range config.Parsing.GroupPatterns {
		regex, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		
		matches := regex.FindStringSubmatch(filename)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return "Unknown"
}

func findOrCreateSeries(anime *ParsedAnime) (int, error) {
	// First, try to find existing series
	seriesID, err := findExistingSeries(anime.Title)
	if err == nil && seriesID > 0 {
		logInfo(fmt.Sprintf("Found existing series: %s (ID: %d)", anime.Title, seriesID))
		return seriesID, nil
	}

	logInfo(fmt.Sprintf("Series not found, searching TVDB for: %s", anime.Title))

	// Search for series on TVDB via Sonarr
	seriesOptions, err := searchSeries(anime.Title)
	if err != nil {
		return 0, fmt.Errorf("failed to search for series: %w", err)
	}

	if len(seriesOptions) == 0 {
		return 0, fmt.Errorf("no series found for: %s", anime.Title)
	}

	// Take the first result (you might want to implement better matching logic)
	selectedSeries := seriesOptions[0]
	logInfo(fmt.Sprintf("Found series option: %s (%d)", selectedSeries.Title, selectedSeries.Year))

	// Add series to Sonarr
	return addSeries(selectedSeries, anime)
}

func findExistingSeries(title string) (int, error) {
	url := fmt.Sprintf("%s/api/v3/series", strings.TrimRight(config.Sonarr.URL, "/"))
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("X-Api-Key", config.Sonarr.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var series []Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return 0, err
	}

	// Simple title matching (you might want to improve this)
	cleanTitle := strings.ToLower(strings.TrimSpace(title))
	for _, s := range series {
		if strings.ToLower(s.Title) == cleanTitle || strings.ToLower(s.SortTitle) == cleanTitle {
			return s.ID, nil
		}
	}

	return 0, fmt.Errorf("series not found")
}

func searchSeries(title string) ([]SeriesLookup, error) {
	url := fmt.Sprintf("%s/api/v3/series/lookup?term=%s", strings.TrimRight(config.Sonarr.URL, "/"), title)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Api-Key", config.Sonarr.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var results []SeriesLookup
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}

	return results, nil
}

func addSeries(seriesLookup SeriesLookup, anime *ParsedAnime) (int, error) {
	series := Series{
		Title:             seriesLookup.Title,
		SortTitle:         seriesLookup.SortTitle,
		Status:            seriesLookup.Status,
		Overview:          seriesLookup.Overview,
		Network:           seriesLookup.Network,
		Images:            seriesLookup.Images,
		Seasons:           seriesLookup.Seasons,
		Year:              seriesLookup.Year,
		Path:              filepath.Join(config.Sonarr.RootFolder, seriesLookup.TitleSlug),
		QualityProfileID:  config.Sonarr.QualityProfile,
		LanguageProfileID: config.Sonarr.LanguageProfile,
		SeasonFolder:      true,
		Monitored:         true,
		UseSceneNumbering: false,
		TvdbID:            seriesLookup.TvdbId,
		TitleSlug:         seriesLookup.TitleSlug,
		RootFolderPath:    config.Sonarr.RootFolder,
		Genres:            seriesLookup.Genres,
		Tags:              []int{},
		AddOptions: AddOptions{
			IgnoreEpisodesWithFiles:    false,
			IgnoreEpisodesWithoutFiles: false,
			SearchForMissingEpisodes:   false,
		},
	}

	jsonData, err := json.Marshal(series)
	if err != nil {
		return 0, err
	}

	url := fmt.Sprintf("%s/api/v3/series", strings.TrimRight(config.Sonarr.URL, "/"))
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", config.Sonarr.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("failed to add series, status: %d", resp.StatusCode)
	}

	var addedSeries Series
	if err := json.NewDecoder(resp.Body).Decode(&addedSeries); err != nil {
		return 0, err
	}

	logInfo(fmt.Sprintf("Added new series: %s (ID: %d)", addedSeries.Title, addedSeries.ID))
	return addedSeries.ID, nil
}

func findEpisode(seriesID, seasonNumber, episodeNumber int) (int, error) {
	url := fmt.Sprintf("%s/api/v3/episode?seriesId=%d", strings.TrimRight(config.Sonarr.URL, "/"), seriesID)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("X-Api-Key", config.Sonarr.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var episodes []Episode
	if err := json.NewDecoder(resp.Body).Decode(&episodes); err != nil {
		return 0, err
	}

	for _, episode := range episodes {
		if episode.SeasonNumber == seasonNumber && episode.EpisodeNumber == episodeNumber {
			return episode.ID, nil
		}
	}

	return 0, fmt.Errorf("episode S%02dE%02d not found", seasonNumber, episodeNumber)
}

func manualImport(anime *ParsedAnime, seriesID, episodeID int) error {
	importFile := ManualImportFile{
		Path:         anime.FilePath,
		SeriesID:     seriesID,
		SeasonNumber: anime.Season,
		Episodes:     []int{episodeID},
		Quality: Quality{
			ID:   1, // You might want to determine this based on anime.Quality
			Name: "HDTV-1080p",
		},
		Language: Language{
			ID:   1,
			Name: "English",
		},
	}

	request := ManualImportRequest{
		Files: []ManualImportFile{importFile},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v3/manualimport", strings.TrimRight(config.Sonarr.URL, "/"))
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", config.Sonarr.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("manual import failed, status: %d", resp.StatusCode)
	}

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
