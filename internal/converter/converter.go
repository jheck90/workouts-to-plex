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

const defaultTimer        = 60
const defaultTotalMinutes = 60

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

// Convert uses the default 60s cycle and 60-minute total duration.
func (c *Converter) Convert(inputPath string) error {
	return c.ConvertWithTimer(inputPath, defaultTimer)
}

// ConvertWithTimer uses a caller-supplied cycle length with default 60-minute total duration.
func (c *Converter) ConvertWithTimer(inputPath string, timerSeconds int) error {
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(c.outputDir, base+".mp4")
	return c.ConvertTo(inputPath, outputPath, timerSeconds, defaultTotalMinutes)
}

// ConvertTo converts to an explicit output path with full control over cycle and duration.
func (c *Converter) ConvertTo(inputPath, outputPath string, timerSeconds, totalMinutes int) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("skipping %s — output already exists", filepath.Base(inputPath))
		return nil
	}

	totalSeconds := totalMinutes * 60
	log.Printf("converting %s -> %s (%ds cycle, %d min total)", filepath.Base(inputPath), filepath.Base(outputPath), timerSeconds, totalMinutes)

	// Looping countdown: 60-mod(t,cycle) resets every cycle
	timerExpr := fmt.Sprintf(
		"drawtext=fontfile=/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf:"+
			"fontsize=72:fontcolor=white:"+
			"box=1:boxcolor=black@0.6:boxborderw=10:"+
			"text='%%{eif\\:%d-mod(t\\,%d)\\:d}':"+
			"x=w-tw-30:y=h-th-30",
		timerSeconds, timerSeconds,
	)

	// Three short beeps at the last 3 seconds of each cycle
	b1, b2, b3 := timerSeconds-3, timerSeconds-2, timerSeconds-1
	beepFilter := fmt.Sprintf(
		"aevalsrc=0.5*sin(2*PI*1000*t)*(between(mod(t\\,%d)\\,%d\\,%d.4)+between(mod(t\\,%d)\\,%d\\,%d.4)+between(mod(t\\,%d)\\,%d\\,%d.4)):s=44100:c=mono",
		timerSeconds, b1, b1,
		timerSeconds, b2, b2,
		timerSeconds, b3, b3,
	)

	args := []string{
		"-y",
		"-loop", "1", "-i", inputPath,
		"-f", "lavfi", "-i", beepFilter,
		"-vf", timerExpr,
		"-t", fmt.Sprintf("%d", totalSeconds),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-r", "1", // 1 fps — static image, saves space
		"-c:a", "aac",
		"-b:a", "64k",
		"-movflags", "+faststart",
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
