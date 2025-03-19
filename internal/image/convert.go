package image

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Achno/gowall/config"
	haldclut "github.com/Achno/gowall/internal/backends/colorthief/haldClut"
	"github.com/Achno/gowall/utils"
)

var clutMutex sync.Mutex

// Constants for file operations
const (
	dirPermissions = 0755 // Directory creation permissions
)

// ThemeConverter handles the conversion of images using color themes
type ThemeConverter struct {
}

// Process applies a color theme to an image and returns the transformed image
// The level parameter controls the quality/detail of the color transformation
// Higher levels provide more accurate color mapping but take longer to process
func (themeConv *ThemeConverter) Process(img image.Image, theme string) (image.Image, error) {
	level := 8

	selectedTheme, err := SelectTheme(theme)
	if err != nil {
		return nil, fmt.Errorf("theme selection error: %w", err)
	}

	// Use NearestNeighbour backend if specified in the config
	if config.GowallConfig.ColorCorrectionBackend == "nn" {
		return NearestNeighbour(img, selectedTheme)
	}

	// Get or create output directory for CLUTs
	dirFolder, err := utils.CreateDirectory()
	if err != nil {
		return nil, fmt.Errorf("creating directory: %w", err)
	}

	// Get theme colors and create a hash to identify the CLUT file
	themeColors, err := GetThemeColors(theme)
	if err != nil {
		return nil, fmt.Errorf("getting theme colors: %w", err)
	}
	colorHash := hashPalette(themeColors)

	// Create a safe filename for the CLUT
	clutFilename := createSafeClutFilename(theme, colorHash)
	clutPath := filepath.Join(dirFolder, "cluts", clutFilename)

	// Generate CLUT if it doesn't exist
	if err := ensureClutExists(clutPath, selectedTheme, level); err != nil {
		return nil, err
	}

	// Load the CLUT file
	clut, err := haldclut.LoadHaldCLUT(clutPath)
	if err != nil {
		return nil, fmt.Errorf("loading CLUT: %w", err)
	}
	if clut == nil {
		return nil, fmt.Errorf("CLUT is nil after loading")
	}

	// Apply the CLUT to the image
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	newImg := haldclut.ApplyCLUT(rgba, clut, level)

	return newImg, nil
}

// createSafeClutFilename creates a safe filename for the CLUT based on the theme name/path
// If the theme is a file path, it extracts just the base name to avoid path issues
// Returns a filename combining the sanitized theme name and a hash of the colors
func createSafeClutFilename(theme, hash string) string {
	// If the theme is a file path, extract just the base name
	themeName := theme
	if isLikelyPath(theme) {
		baseName := filepath.Base(theme)
		themeName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
	}

	return fmt.Sprintf("%s_%s.png", themeName, hash)
}

// isLikelyPath checks if a string appears to be a file path
// Returns true if the string contains path-like characters or patterns
func isLikelyPath(s string) bool {
	return filepath.IsAbs(s) || strings.HasPrefix(s, "~") || strings.Contains(s, "/") || strings.Contains(s, "\\")
}

// ensureClutExists generates a CLUT file if it doesn't already exist
// Uses lock to prevent race conditions when multiple goroutines try to create the same file
// Returns an error if the file cannot be created or the CLUT generation fails
func ensureClutExists(clutPath string, theme Theme, level int) error {
	clutMutex.Lock()
	defer clutMutex.Unlock()

	// If CLUT already exists, we can use it
	if _, err := os.Stat(clutPath); err == nil {
		return nil
	}

	// Create parent directory if needed
	clutDir := filepath.Dir(clutPath)
	if err := os.MkdirAll(clutDir, dirPermissions); err != nil {
		return fmt.Errorf("creating CLUT directory: %w", err)
	}

	// Generate identity CLUT
	identityClut, err := haldclut.GenerateIdentityCLUT(level)
	if err != nil {
		return fmt.Errorf("generating identity CLUT: %w", err)
	}

	// Convert theme colors to RGBA format
	palette, err := toRGBA(theme.Colors)
	if err != nil {
		return fmt.Errorf("converting colors to RGBA: %w", err)
	}

	// Create the modified CLUT
	mapper := &haldclut.RBFMapper{}
	modifiedClut := haldclut.InterpolateCLUT(identityClut, palette, level, mapper)

	// Save the CLUT to disk
	if err := haldclut.SaveHaldCLUT(modifiedClut, clutPath); err != nil {
		return fmt.Errorf("saving CLUT: %w", err)
	}

	return nil
}

// NearestNeighbour transforms an image by mapping each pixel to the closest color in the theme
// This is a simpler but potentially faster alternative to CLUT-based color mapping
// It works by finding the closest theme color for each pixel in the image
func NearestNeighbour(img image.Image, theme Theme) (image.Image, error) {
	bounds := img.Bounds()
	newImg := image.NewRGBA(bounds)

	// Replace each pixel with the selected theme's nearest color
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			originalColor := img.At(x, y)
			newColor := nearestColor(originalColor, theme)
			newImg.Set(x, y, newColor)
		}
	}

	if newImg == nil {
		return nil, errors.New("error processing the Image")
	}

	return newImg, nil
}

// nearestColor finds the closest color in the theme to the given input color
// It computes the perceptual distance between the input color and each theme color
// and returns the theme color with the smallest distance
func nearestColor(clr color.Color, theme Theme) color.Color {
	r, g, b, _ := clr.RGBA()

	// Convert from 16-bit to 8-bit
	r, g, b = r>>8, g>>8, b>>8

	minDist := math.MaxFloat64
	var nearestClr color.Color

	for _, themeColor := range theme.Colors {
		tr, tg, tb, _ := themeColor.RGBA()
		// Convert from 16-bit to 8-bit
		tr, tg, tb = tr>>8, tg>>8, tb>>8

		distance := colorDistance(tr, tg, tb, r, g, b)

		if distance < minDist {
			minDist = distance
			nearestClr = themeColor
		}
	}

	return nearestClr
}

// colorDistance calculates the perceptual distance between two colors using a weighted approach
// This approximates human perception by giving green more weight than red, and red more than blue
func colorDistance(r1, g1, b1, r2, g2, b2 uint32) float64 {
	// Weighted RGB components based on human perception
	// These weights approximate human color perception sensitivity
	// Humans are most sensitive to green, then red, then blue
	const (
		redWeight   = 0.299
		greenWeight = 0.587
		blueWeight  = 0.114
	)

	// Calculate squared differences with perceptual weights
	rDiff := float64(r1) - float64(r2)
	gDiff := float64(g1) - float64(g2)
	bDiff := float64(b1) - float64(b2)

	weightedDistance := redWeight*rDiff*rDiff +
		greenWeight*gDiff*gDiff +
		blueWeight*bDiff*bDiff

	return math.Sqrt(weightedDistance)
}

// toRGBA converts a slice of color.Color to a slice of color.RGBA
// Returns an error if any color in the slice is not of type color.RGBA
func toRGBA(clrs []color.Color) ([]color.RGBA, error) {
	rgbaColors := make([]color.RGBA, len(clrs))

	for i, c := range clrs {
		if rgba, ok := c.(color.RGBA); ok {
			rgbaColors[i] = rgba
		} else {
			return nil, fmt.Errorf("while converting theme color at index %d is not color.RGBA: %T", i, c)
		}
	}

	return rgbaColors, nil
}

// hashPalette creates a hash from a slice of color strings
// This is used to uniquely identify a color palette for CLUT caching
func hashPalette(colors []string) string {
	hasher := md5.New()
	for _, color := range colors {
		hasher.Write([]byte(color))
	}
	// Use constant value 16 directly to avoid cross-file constant reference issues
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}
