package image

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Achno/gowall/config"
	"gopkg.in/yaml.v2"
)

// Theme represents a color theme for image transformation
type Theme struct {
	Name   string
	Colors []color.Color
}

// ThemeData represents the structure of an external theme file
type ThemeData struct {
	Name   string   `json:"name" yaml:"name"`
	Colors []string `json:"colors" yaml:"colors"`
}

// Map of all available themes
var themes = make(map[string]Theme)

// Default theme directories to search
var themeDirectories = []string{
	"themes",                  // Local themes directory
	"~/.config/gowall/themes", // User themes directory
	"~/.emacs.d/themes",       // Emacs themes directory
}

// Precompiled regex patterns for extracting colors from Emacs themes
var (
	emacsThemeColorPatterns = []struct {
		description string
		regex       *regexp.Regexp
	}{
		{
			"Standard hex color",
			regexp.MustCompile(`(?i)(?:[:#]|[^\\]")(#[0-9A-Fa-f]{6})(?:[^0-9A-Fa-f]|$)`),
		},
		{
			"Variable assignments",
			regexp.MustCompile(`\(setq [^)]*?(?:fg|bg|face)[^)]*?"(#[0-9A-Fa-f]{6})"`),
		},
		{
			"Face properties",
			regexp.MustCompile(`\(:(?:fore|back)ground +"(#[0-9A-Fa-f]{6})"`),
		},
		{
			"Color specs",
			regexp.MustCompile(`"(?:fore|back)ground"[^)]*?(#[0-9A-Fa-f]{6})`),
		},
		{
			"Constants and variables",
			regexp.MustCompile(`\(def(?:const|var)[^)]*?"(#[0-9A-Fa-f]{6})"`),
		},
	}
)

// Constants for theme generation
const (
	// File permissions
	FilePermissions = 0644
	DirPermissions  = 0755

	// Emacs theme constants
	EmacsMinColors = 89 // Minimum colors for Emacs themes

	// Color constants
	HexColorLength    = 7 // Length of hex color strings including '#'
	HexColorPrefixPos = 0 // Position of '#' in hex color strings

	// Hash constants
	HashLength = 16 // Length of the color hash
)

// init loads all themes from files when the package is initialized
func init() {
	loadExternalThemes()
	loadCustomThemes() // Load from config.yml (for backward compatibility)

	// If no themes were loaded, add a basic default theme as fallback
	if len(themes) == 0 {
		themes["default"] = Theme{
			Name: "Default",
			Colors: []color.Color{
				color.RGBA{R: 0, G: 0, B: 0, A: 255},       // Black
				color.RGBA{R: 255, G: 255, B: 255, A: 255}, // White
				color.RGBA{R: 255, G: 0, B: 0, A: 255},     // Red
				color.RGBA{R: 0, G: 255, B: 0, A: 255},     // Green
				color.RGBA{R: 0, G: 0, B: 255, A: 255},     // Blue
			},
		}
		log.Println("No themes found, using minimal default theme")
	}
}

// loadExternalThemes loads themes from external JSON/YAML/Emacs files
func loadExternalThemes() {
	for _, dirPath := range themeDirectories {
		dirPath = expandPath(dirPath)
		if dirPath == "" {
			continue
		}

		// Check if directory exists
		_, err := os.Stat(dirPath)
		if os.IsNotExist(err) {
			// Create the directory if it doesn't exist (only for gowall directories)
			if strings.Contains(dirPath, "gowall") {
				if err := os.MkdirAll(dirPath, DirPermissions); err != nil {
					log.Printf("failed to create theme directory %s: %v", dirPath, err)
					continue
				}
				log.Printf("created theme directory: %s", dirPath)
			} else {
				// Skip non-gowall directories that don't exist
				continue
			}
		} else if err != nil {
			// Handle other stat errors
			log.Printf("error checking theme directory %s: %v", dirPath, err)
			continue
		}

		// Read all files in the directory
		files, err := os.ReadDir(dirPath)
		if err != nil {
			log.Printf("error reading theme directory %s: %v", dirPath, err)
			continue
		}

		// Process each file
		for _, file := range files {
			if file.IsDir() {
				continue
			}

			filePath := filepath.Join(dirPath, file.Name())
			ext := strings.ToLower(filepath.Ext(file.Name()))

			// Process based on file extension
			switch ext {
			case ".json", ".yaml", ".yml":
				loadJSONYAMLTheme(filePath, ext)
			case ".el":
				loadEmacsTheme(filePath)
			}
		}
	}
}

// expandPath expands a path with a tilde prefix to an absolute path
// Returns empty string on error
func expandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("error getting home directory: %v", err)
		return ""
	}

	return filepath.Join(home, path[1:])
}

// loadJSONYAMLTheme loads a theme from a JSON or YAML file
func loadJSONYAMLTheme(filePath, ext string) {
	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("error reading theme file %s: %v", filePath, err)
		return
	}

	// Parse the file
	var themeData ThemeData
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &themeData); err != nil {
			log.Printf("error parsing JSON theme file %s: %v", filePath, err)
			return
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &themeData); err != nil {
			log.Printf("error parsing YAML theme file %s: %v", filePath, err)
			return
		}
	}

	// Validate theme
	if themeData.Name == "" || len(themeData.Colors) == 0 {
		log.Printf("invalid theme in %s: missing name or colors", filePath)
		return
	}

	// Convert hex colors to RGBA
	rgbaColors := make([]color.Color, 0, len(themeData.Colors))
	for _, hexColor := range themeData.Colors {
		rgba, err := HexToRGBA(hexColor)
		if err != nil {
			log.Printf("invalid color %s in theme %s (%s): %v",
				hexColor, themeData.Name, filePath, err)
			return
		}
		rgbaColors = append(rgbaColors, rgba)
	}

	// Add theme to map (overwrite existing if same name)
	themeName := strings.ToLower(themeData.Name)
	themes[themeName] = Theme{
		Name:   themeData.Name,
		Colors: rgbaColors,
	}
	log.Printf("loaded theme from %s: %s", ext, themeData.Name)
}

// loadEmacsTheme loads a theme from an Emacs theme file (.el)
func loadEmacsTheme(filePath string) {
	// Open the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("error reading Emacs theme file %s: %v", filePath, err)
		return
	}

	fileContent := string(data)

	// Extract theme name from filename
	baseName := filepath.Base(filePath)
	themeName := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	themeName = strings.ReplaceAll(themeName, "-theme", "")
	themeName = strings.ReplaceAll(themeName, "-", " ")
	// Title case the name (first letter of each word capitalized)
	themeName = strings.Title(themeName)

	// Set of patterns to extract colors from Emacs themes
	hexColors := extractEmacsThemeColors(fileContent)
	if len(hexColors) == 0 {
		log.Printf("no valid colors found in Emacs theme %s", filePath)
		return
	}

	// Convert hex colors to RGBA
	rgbaColors := make([]color.Color, 0, len(hexColors))
	for hexColor := range hexColors {
		rgba, err := HexToRGBA(hexColor)
		if err != nil {
			log.Printf("invalid color %s in theme %s: %v", hexColor, themeName, err)
			continue
		}
		rgbaColors = append(rgbaColors, rgba)
	}

	// Add the theme with the normalized name
	themeKey := strings.ToLower(themeName)
	themes[themeKey] = Theme{
		Name:   themeName,
		Colors: rgbaColors,
	}
	log.Printf("loaded Emacs theme: %s with %d colors", themeName, len(rgbaColors))

	// Also register by filepath (case insensitive) to handle direct file references
	filePathKey := strings.ToLower(filePath)
	themes[filePathKey] = Theme{
		Name:   themeName + " (from " + baseName + ")",
		Colors: rgbaColors,
	}
}

// extractEmacsThemeColors extracts unique hex color codes from Emacs theme content
func extractEmacsThemeColors(content string) map[string]struct{} {
	// Track unique colors using a map as a set
	uniqueColors := make(map[string]struct{})

	// Apply each pattern to find colors
	for _, patternInfo := range emacsThemeColorPatterns {
		matches := patternInfo.regex.FindAllStringSubmatch(content, -1)

		for _, match := range matches {
			if len(match) < 2 {
				continue
			}

			hexColor := match[1]

			// Validate the hex color format
			if !strings.HasPrefix(hexColor, "#") || len(hexColor) != HexColorLength {
				continue
			}

			// Add to our unique colors set
			uniqueColors[hexColor] = struct{}{}
		}
	}

	return uniqueColors
}

// loadCustomThemes loads themes from config.yml (for backward compatibility)
func loadCustomThemes() {
	for _, tw := range config.GowallConfig.Themes {
		// Skip invalid themes
		if tw.Name == "" || len(tw.Colors) == 0 {
			continue
		}

		theme := Theme{
			Name:   tw.Name,
			Colors: make([]color.Color, len(tw.Colors)),
		}

		valid := true
		for i, hexColor := range tw.Colors {
			col, err := HexToRGBA(hexColor)
			if err != nil {
				log.Printf("invalid color %s in theme %s: %v", hexColor, tw.Name, err)
				valid = false
				break
			}
			theme.Colors[i] = col
		}

		if valid {
			themeName := strings.ToLower(tw.Name)
			themes[themeName] = theme
			log.Printf("loaded custom theme from config.yml: %s", tw.Name)
		}
	}
}

// SaveThemeToFile saves a theme to an external file in the specified format
func SaveThemeToFile(theme Theme, format string) error {
	// Get appropriate theme directory based on format
	themeDir, err := getThemeDirectory(format)
	if err != nil {
		return err
	}

	// Create theme directory if it doesn't exist
	if err := os.MkdirAll(themeDir, DirPermissions); err != nil {
		return fmt.Errorf("creating theme directory %s: %w", themeDir, err)
	}

	// Convert colors to hex strings
	hexColors, err := themeColorsToHex(theme.Colors)
	if err != nil {
		return err
	}

	// Generate file content based on format
	filePath, data, err := generateThemeFile(themeDir, theme.Name, hexColors, format)
	if err != nil {
		return err
	}

	// Write to file
	if err := os.WriteFile(filePath, data, FilePermissions); err != nil {
		return fmt.Errorf("writing theme file: %w", err)
	}

	return nil
}

// getThemeDirectory returns the appropriate directory for the theme based on format
func getThemeDirectory(format string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}

	format = strings.ToLower(format)
	if format == "emacs" || format == "el" {
		return filepath.Join(home, ".emacs.d", "themes"), nil
	}

	return filepath.Join(home, ".config", "gowall", "themes"), nil
}

// themeColorsToHex converts a slice of color.Color to hex strings
func themeColorsToHex(colors []color.Color) ([]string, error) {
	hexColors := make([]string, 0, len(colors))
	for _, clr := range colors {
		rgba, ok := clr.(color.RGBA)
		if !ok {
			return nil, fmt.Errorf("color is not of type color.RGBA")
		}
		hexColors = append(hexColors, RGBtoHex(rgba))
	}
	return hexColors, nil
}

// generateThemeFile creates the theme file content based on format
func generateThemeFile(dir, themeName string, hexColors []string, format string) (string, []byte, error) {
	var filePath string
	var data []byte
	var err error

	format = strings.ToLower(format)
	themeNameLower := strings.ToLower(themeName)

	switch format {
	case "json":
		filePath = filepath.Join(dir, themeNameLower+".json")
		data, err = generateJSONTheme(themeName, hexColors)

	case "yaml", "yml":
		filePath = filepath.Join(dir, themeNameLower+".yaml")
		data, err = generateYAMLTheme(themeName, hexColors)

	case "emacs", "el":
		filePath = filepath.Join(dir, themeNameLower+"-theme.el")
		data = generateEmacsTheme(themeName, hexColors)

	default:
		err = fmt.Errorf("unsupported format: %s", format)
	}

	if err != nil {
		return "", nil, fmt.Errorf("generating theme content: %w", err)
	}

	return filePath, data, nil
}

// generateJSONTheme generates JSON content for a theme
func generateJSONTheme(name string, colors []string) ([]byte, error) {
	themeData := ThemeData{
		Name:   name,
		Colors: colors,
	}
	return json.MarshalIndent(themeData, "", "  ")
}

// generateYAMLTheme generates YAML content for a theme
func generateYAMLTheme(name string, colors []string) ([]byte, error) {
	themeData := ThemeData{
		Name:   name,
		Colors: colors,
	}
	return yaml.Marshal(themeData)
}

// generateEmacsTheme generates Emacs Lisp content for a theme
func generateEmacsTheme(themeName string, hexColors []string) []byte {
	var content strings.Builder
	themeNameLower := strings.ToLower(themeName)

	// Header with standard Emacs package metadata
	content.WriteString(";;; " + themeNameLower + "-theme.el --- " + themeName + " theme for Emacs\n\n")
	content.WriteString(";; Author: Generated by gowall\n")
	content.WriteString(";; Version: 1.0\n")
	content.WriteString(";; Keywords: faces\n\n")
	content.WriteString(";; This file is not part of GNU Emacs.\n\n")

	// Theme definition
	content.WriteString(";;; Commentary:\n")
	content.WriteString(";;  Color theme generated by gowall based on " + themeName + "\n\n")
	content.WriteString(";;; Code:\n\n")
	content.WriteString("(deftheme " + themeNameLower + "\n")
	content.WriteString("  \"A color theme generated by gowall based on " + themeName + ".\")\n\n")

	// Create a let binding for the colors
	content.WriteString("(let ((class '((class color) (min-colors " + fmt.Sprintf("%d", EmacsMinColors) + ")))\n")

	// Define color variables for better organization
	for i, hexColor := range hexColors {
		varName := fmt.Sprintf("      (color-%d \"%s\")", i, hexColor)
		content.WriteString(varName)
		if i < len(hexColors)-1 {
			content.WriteString("\n")
		}
	}
	content.WriteString(")\n\n")

	// Define face customizations
	content.WriteString("  (custom-theme-set-faces\n")
	content.WriteString("   '" + themeNameLower + "\n")

	// Map the colors to appropriate Emacs faces
	defineEmacsFaces(&content, hexColors)

	// Close custom-theme-set-faces
	content.WriteString("   )\n")

	// Close the let binding
	content.WriteString(")\n\n")

	// Add the provide statement
	content.WriteString("(provide-theme '" + themeNameLower + ")\n")
	content.WriteString("(provide '" + themeNameLower + "-theme)\n\n")
	content.WriteString(";;; " + themeNameLower + "-theme.el ends here\n")

	return []byte(content.String())
}

// defineEmacsFaces writes face definitions to the theme content
func defineEmacsFaces(content *strings.Builder, hexColors []string) {
	numColors := len(hexColors)
	faceDefinitions := []struct {
		minColors int
		faceName  string
		propType  string
		colorIdx  int
	}{
		{1, "default", "foreground", 0},
		{2, "cursor", "background", 1},
		{3, "fringe", "background", 2},
		{4, "highlight", "background", 3},
		{5, "region", "background", 4},
		{6, "font-lock-builtin-face", "foreground", 5},
		{7, "font-lock-comment-face", "foreground", 6},
		{8, "font-lock-function-name-face", "foreground", 7},
		{9, "font-lock-keyword-face", "foreground", 8},
		{10, "font-lock-string-face", "foreground", 9},
		{11, "font-lock-type-face", "foreground", 10},
		{12, "font-lock-variable-name-face", "foreground", 11},
	}

	// Add standard faces
	for _, face := range faceDefinitions {
		if numColors >= face.minColors {
			fmt.Fprintf(content, "   `(%s ((,class (:%s ,color-%d))))\n",
				face.faceName, face.propType, face.colorIdx)
		}
	}

	// Add fallbacks for themes with fewer colors
	if numColors < 12 {
		addFallbackFaces(content, numColors)
	}
}

// addFallbackFaces adds fallback face definitions for themes with fewer colors
func addFallbackFaces(content *strings.Builder, numColors int) {
	// Use modulo to reuse available colors
	fallbacks := []struct {
		faceName string
		offset   int
	}{
		{"font-lock-function-name-face", 0},
		{"font-lock-keyword-face", 1},
		{"font-lock-string-face", 2},
		{"font-lock-type-face", 3},
		{"font-lock-variable-name-face", 4},
	}

	for _, fb := range fallbacks {
		colorIdx := (numColors + fb.offset) % numColors
		fmt.Fprintf(content, "   `(%s ((,class (:foreground ,color-%d))))\n",
			fb.faceName, colorIdx)
	}
}

// HexToRGBA converts a hex color string to color.RGBA
func HexToRGBA(hexStr string) (color.RGBA, error) {
	if len(hexStr) != HexColorLength || hexStr[HexColorPrefixPos] != '#' {
		return color.RGBA{}, errors.New("invalid hex color format")
	}
	bytes, err := hex.DecodeString(hexStr[1:])
	if err != nil {
		return color.RGBA{}, err
	}
	return color.RGBA{R: bytes[0], G: bytes[1], B: bytes[2], A: 255}, nil
}

// HexToRGBASlice converts a slice of hex color strings to color.Color slice
func HexToRGBASlice(hexColors []string) ([]color.Color, error) {
	rgbaColors := make([]color.Color, 0, len(hexColors))
	for _, hex := range hexColors {
		rgba, err := HexToRGBA(hex)
		if err != nil {
			return nil, err
		}
		rgbaColors = append(rgbaColors, rgba)
	}
	return rgbaColors, nil
}

// RGBtoHex converts a color.RGBA to hex string
func RGBtoHex(c color.RGBA) string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// ListThemes returns a slice of all available theme names
func ListThemes() []string {
	allThemes := make([]string, 0, len(themes))
	for theme := range themes {
		allThemes = append(allThemes, theme)
	}
	return allThemes
}

// SelectTheme returns a theme by name or an error if not found
func SelectTheme(theme string) (Theme, error) {
	// Check if the theme already exists by name
	themeLower := strings.ToLower(theme)
	if selectedTheme, exists := themes[themeLower]; exists {
		return selectedTheme, nil
	}

	// Check if it's a file path to an Emacs theme
	if isEmacsThemeFile(theme) {
		loadEmacsTheme(theme)

		// Check if loading was successful
		themeLower = strings.ToLower(theme)
		if selectedTheme, exists := themes[themeLower]; exists {
			return selectedTheme, nil
		}
	}

	// Try expanding tilde if present
	if strings.HasPrefix(theme, "~") {
		expandedPath := expandPath(theme)
		if expandedPath != "" && isEmacsThemeFile(expandedPath) {
			loadEmacsTheme(expandedPath)

			// Check if loading was successful
			expandedPathLower := strings.ToLower(expandedPath)
			if selectedTheme, exists := themes[expandedPathLower]; exists {
				return selectedTheme, nil
			}
		}
	}

	// Unable to find or load the theme
	return Theme{}, fmt.Errorf("unknown theme: %s", theme)
}

// isEmacsThemeFile checks if a path points to an existing .el file
func isEmacsThemeFile(path string) bool {
	// Check if file exists
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	// Check extension
	return strings.ToLower(filepath.Ext(path)) == ".el"
}

// ThemeExists checks if a theme exists by name
func ThemeExists(theme string) bool {
	_, exists := themes[strings.ToLower(theme)]
	return exists
}

// GetThemeColors returns the colors of a theme in hex code format
func GetThemeColors(theme string) ([]string, error) {
	selectedTheme, err := SelectTheme(theme)
	if err != nil {
		return nil, err
	}

	colors := make([]string, 0, len(selectedTheme.Colors))
	for _, clr := range selectedTheme.Colors {
		rgba, ok := clr.(color.RGBA)
		if !ok {
			return nil, fmt.Errorf("color is not of type color.RGBA")
		}
		hexCode := RGBtoHex(rgba)
		colors = append(colors, hexCode)
	}

	return colors, nil
}
