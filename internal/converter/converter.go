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

type Converter struct {
	outputDir    string
	timerSeconds int
}

func New(outputDir string, timerSeconds int) *Converter {
	return &Converter{
		outputDir:    outputDir,
		timerSeconds: timerSeconds,
	}
}

func (c *Converter) IsSupported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return supportedExtensions[ext]
}

func (c *Converter) Convert(inputPath string) error {
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(c.outputDir, base+".mp4")

	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("skipping %s — output already exists", filepath.Base(inputPath))
		return nil
	}

	log.Printf("converting %s -> %s (%ds timer)", filepath.Base(inputPath), filepath.Base(outputPath), c.timerSeconds)

	timerExpr := fmt.Sprintf(
		"drawtext=fontfile=/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf:"+
			"fontsize=72:fontcolor=white:"+
			"box=1:boxcolor=black@0.6:boxborderw=10:"+
			"text='%%{eif\\:%d-t\\:d}':"+
			"x=w-tw-30:y=h-th-30",
		c.timerSeconds,
	)

	args := []string{
		"-y",
		"-loop", "1",
		"-i", inputPath,
		"-vf", timerExpr,
		"-t", fmt.Sprintf("%d", c.timerSeconds),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-r", "1", // 1 fps — static image, saves space
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed for %s: %w", inputPath, err)
	}

	log.Printf("done: %s", outputPath)
	return nil
}
