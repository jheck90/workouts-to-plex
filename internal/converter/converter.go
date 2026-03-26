package converter

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var supportedExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
}

const defaultTimer = 60

type Converter struct {
	outputDir string
}

func New(outputDir string) *Converter {
	return &Converter{outputDir: outputDir}
}

func (c *Converter) IsSupported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return supportedExtensions[ext]
}

// Convert uses the default 60s timer, outputting into the configured output dir.
func (c *Converter) Convert(inputPath string) error {
	return c.ConvertWithTimer(inputPath, defaultTimer)
}

// ConvertWithTimer uses a caller-supplied timer, outputting into the configured output dir.
func (c *Converter) ConvertWithTimer(inputPath string, timerSeconds int) error {
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(c.outputDir, base+".mp4")
	return c.ConvertTo(inputPath, outputPath, timerSeconds)
}

// ConvertTo converts to an explicit output path, creating parent directories as needed.
func (c *Converter) ConvertTo(inputPath, outputPath string, timerSeconds int) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("skipping %s — output already exists", filepath.Base(inputPath))
		return nil
	}

	log.Printf("converting %s -> %s (%ds timer)", filepath.Base(inputPath), filepath.Base(outputPath), timerSeconds)

	timerExpr := fmt.Sprintf(
		"drawtext=fontfile=/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf:"+
			"fontsize=72:fontcolor=white:"+
			"box=1:boxcolor=black@0.6:boxborderw=10:"+
			"text='%%{eif\\:%d-t\\:d}':"+
			"x=w-tw-30:y=h-th-30",
		timerSeconds,
	)

	args := []string{
		"-y",
		"-loop", "1",
		"-i", inputPath,
		"-vf", timerExpr,
		"-t", fmt.Sprintf("%d", timerSeconds),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-r", "1", // 1 fps — static image, saves space
		"-movflags", "+faststart", // move moov atom to front for streaming
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Remove(outputPath) // clean up partial file so next run retries
		return fmt.Errorf("ffmpeg failed for %s: %w", inputPath, err)
	}

	log.Printf("done: %s", outputPath)
	return nil
}
