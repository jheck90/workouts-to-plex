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

func (c *Converter) Convert(inputPath string) error {
	return c.ConvertWithTimer(inputPath, defaultTimer)
}

func (c *Converter) ConvertWithTimer(inputPath string, timerSeconds int) error {
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(c.outputDir, base+".mp4")
	return c.ConvertTo(inputPath, outputPath, timerSeconds, defaultTotalMinutes)
}

// ConvertTo encodes one cycle, then stream-copies it to the full duration.
func (c *Converter) ConvertTo(inputPath, outputPath string, timerSeconds, totalMinutes int) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("skipping %s — output already exists", filepath.Base(inputPath))
		return nil
	}

	cycleFile := strings.TrimSuffix(outputPath, ".mp4") + ".cycle.mp4"
	defer os.Remove(cycleFile)

	if err := c.encodeCycle(inputPath, cycleFile, timerSeconds); err != nil {
		return err
	}

	cycles := (totalMinutes * 60) / timerSeconds
	if err := c.loopCycle(cycleFile, outputPath, cycles); err != nil {
		os.Remove(outputPath)
		return err
	}

	log.Printf("done: %s", outputPath)
	return nil
}

// encodeCycle encodes a single timerSeconds-long segment with countdown + beeps.
func (c *Converter) encodeCycle(inputPath, outputPath string, timerSeconds int) error {
	log.Printf("encoding %ds cycle from %s", timerSeconds, filepath.Base(inputPath))

	timerExpr := fmt.Sprintf(
		"drawtext=fontfile=/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf:"+
			"fontsize=72:fontcolor=white:"+
			"box=1:boxcolor=black@0.6:boxborderw=10:"+
			"text='%%{eif\\:%d-t\\:d}':"+
			"x=w-tw-30:y=h-th-30",
		timerSeconds,
	)

	b1, b2, b3 := timerSeconds-3, timerSeconds-2, timerSeconds-1
	beepFilter := fmt.Sprintf(
		"aevalsrc=0.5*sin(2*PI*1000*t)*(between(mod(t\\,%d)\\,%d\\,%d.4)+between(mod(t\\,%d)\\,%d\\,%d.4)+between(mod(t\\,%d)\\,%d\\,%d.4)):s=44100:c=mono",
		timerSeconds, b1, b1,
		timerSeconds, b2, b2,
		timerSeconds, b3, b3,
	)

	// GPU_CODEC overrides the encoder: h264_nvenc, h264_vaapi, h264_qsv, etc.
	codec := "libx264"
	if gpu := os.Getenv("GPU_CODEC"); gpu != "" {
		log.Printf("using GPU codec: %s", gpu)
		codec = gpu
	}

	args := []string{
		"-y",
		"-loop", "1", "-i", inputPath,
		"-f", "lavfi", "-i", beepFilter,
		"-vf", timerExpr,
		"-t", fmt.Sprintf("%d", timerSeconds),
		"-c:v", codec,
		"-pix_fmt", "yuv420p",
		"-r", "1",
		"-c:a", "aac",
		"-b:a", "64k",
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("cycle encode failed: %w", err)
	}
	return nil
}

// loopCycle stream-copies the cycle file N times into outputPath — no re-encode.
func (c *Converter) loopCycle(cycleFile, outputPath string, cycles int) error {
	log.Printf("looping cycle %dx -> %s", cycles, filepath.Base(outputPath))

	concatFile := cycleFile + ".txt"
	defer os.Remove(concatFile)

	var sb strings.Builder
	for i := 0; i < cycles; i++ {
		fmt.Fprintf(&sb, "file '%s'\n", cycleFile)
	}
	if err := os.WriteFile(concatFile, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write concat list: %w", err)
	}

	args := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatFile,
		"-c", "copy",
		"-movflags", "+faststart",
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("concat loop failed: %w", err)
	}
	return nil
}
