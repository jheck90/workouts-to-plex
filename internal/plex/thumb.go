package plex

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WriteEpisodeThumb converts srcPNG to a JPEG thumbnail at <outputMP4>-thumb.jpg.
// Plex picks this up automatically as the episode thumbnail when Local Media Assets is enabled.
func WriteEpisodeThumb(outputPath, srcPNG string) error {
	thumbPath := strings.TrimSuffix(outputPath, ".mp4") + "-thumb.jpg"
	if _, err := os.Stat(thumbPath); err == nil {
		log.Printf("skipping thumb — already exists: %s", filepath.Base(thumbPath))
		return nil
	}
	cmd := exec.Command("ffmpeg", "-y",
		"-i", srcPNG,
		"-q:v", "2",
		thumbPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("thumbnail generation failed for %s: %w\n%s", filepath.Base(srcPNG), err, out)
	}
	log.Printf("thumb: %s", filepath.Base(thumbPath))
	return nil
}

// WriteShowPoster copies srcPNG as poster.jpg in the show's category folder.
// Plex uses this as the show/collection poster image.
func WriteShowPoster(categoryDir, srcPNG string) error {
	posterPath := filepath.Join(categoryDir, "poster.jpg")
	if _, err := os.Stat(posterPath); err == nil {
		return nil // already exists, don't overwrite
	}
	if err := os.MkdirAll(categoryDir, 0755); err != nil {
		return fmt.Errorf("create category dir: %w", err)
	}
	cmd := exec.Command("ffmpeg", "-y",
		"-i", srcPNG,
		"-q:v", "2",
		posterPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("poster generation failed: %w\n%s", err, out)
	}
	log.Printf("poster: %s", posterPath)
	return nil
}
