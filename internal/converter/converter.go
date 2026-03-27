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

// Chapter represents a named chapter marker in the output video.
type Chapter struct {
	Title string
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

// ConvertFramesTo encodes each frame as a timerSeconds-long cycle, then
// concatenates the frame sequence repeatedly to fill totalMinutes.
// warmupCount is the number of leading frames that should only appear in the
// first pass (e.g. 1 when the first frame is the warmup slide).
// warmupChapters and repeatingChapters name each frame for the Plex chapter scrubber;
// pass nil to skip chapter embedding.
func (c *Converter) ConvertFramesTo(inputPaths []string, outputPath string, timerSeconds, totalMinutes, warmupCount int, warmupChapters, repeatingChapters []Chapter) error {
	if len(inputPaths) == 0 {
		return fmt.Errorf("no input frames provided")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("skipping %s — output already exists", filepath.Base(outputPath))
		return nil
	}

	// Encode each frame to a cycle file.
	var cycleFiles []string
	for _, p := range inputPaths {
		cycleFile := strings.TrimSuffix(outputPath, ".mp4") + "_" + strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) + ".cycle.mp4"
		if err := c.encodeCycle(p, cycleFile, timerSeconds); err != nil {
			for _, cf := range cycleFiles {
				os.Remove(cf)
			}
			return err
		}
		cycleFiles = append(cycleFiles, cycleFile)
	}
	defer func() {
		for _, cf := range cycleFiles {
			os.Remove(cf)
		}
	}()

	if warmupCount < 0 || warmupCount > len(cycleFiles) {
		warmupCount = 0
	}
	warmupCycles := cycleFiles[:warmupCount]
	repeatingCycles := cycleFiles[warmupCount:]

	// Each pass through the repeating frames takes len(repeating)*timerSeconds seconds.
	// Use all frames for duration calculation on the first pass.
	totalSeconds := totalMinutes * 60
	repeatingDuration := len(repeatingCycles) * timerSeconds
	if repeatingDuration == 0 {
		repeatingDuration = timerSeconds
	}
	passes := totalSeconds / repeatingDuration
	if passes < 1 {
		passes = 1
	}

	if err := c.loopFrames(warmupCycles, repeatingCycles, outputPath, timerSeconds, passes, warmupChapters, repeatingChapters); err != nil {
		os.Remove(outputPath)
		return err
	}

	log.Printf("done: %s", outputPath)
	return nil
}

// loopFrames stream-copies warmupFiles once (first pass only) followed by
// repeatingFiles for `passes` passes. Chapter metadata is embedded when chapters are provided.
func (c *Converter) loopFrames(warmupFiles, repeatingFiles []string, outputPath string, timerSeconds, passes int, warmupChapters, repeatingChapters []Chapter) error {
	log.Printf("concatenating warmup(%d) + %d frames × %d passes -> %s", len(warmupFiles), len(repeatingFiles), passes, filepath.Base(outputPath))

	concatFile := outputPath + ".txt"
	defer os.Remove(concatFile)

	var sb strings.Builder
	for _, cf := range warmupFiles {
		fmt.Fprintf(&sb, "file '%s'\n", cf)
	}
	for i := 0; i < passes; i++ {
		for _, cf := range repeatingFiles {
			fmt.Fprintf(&sb, "file '%s'\n", cf)
		}
	}
	if err := os.WriteFile(concatFile, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write concat list: %w", err)
	}

	args := []string{"-y", "-f", "concat", "-safe", "0", "-i", concatFile}

	// Embed chapter markers when chapter titles are provided.
	metaFile := outputPath + ".meta"
	if len(warmupChapters)+len(repeatingChapters) > 0 {
		if err := writeChapterMetadata(metaFile, len(warmupFiles), len(repeatingFiles), timerSeconds, passes, warmupChapters, repeatingChapters); err != nil {
			log.Printf("warning: chapter metadata skipped: %v", err)
		} else {
			defer os.Remove(metaFile)
			args = append(args, "-i", metaFile, "-map", "0", "-map_metadata", "1")
		}
	}

	args = append(args, "-c", "copy", "-movflags", "+faststart", outputPath)

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("concat frames failed: %w", err)
	}
	return nil
}

// writeChapterMetadata writes an ffmetadata file with chapter timestamps.
// The first pass through repeatingFiles gets named chapters; subsequent passes
// get a single "Cycle N" chapter each.
func writeChapterMetadata(path string, warmupCount, repeatingCount, timerSeconds, passes int, warmupChapters, repeatingChapters []Chapter) error {
	var sb strings.Builder
	sb.WriteString(";FFMETADATA1\n\n")

	ms := 0 // current timestamp in milliseconds
	step := timerSeconds * 1000

	// Warmup — shown once.
	for i := range warmupCount {
		title := "Warmup"
		if i < len(warmupChapters) {
			title = warmupChapters[i].Title
		}
		fmt.Fprintf(&sb, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n\n",
			ms, ms+step, escapeMetadata(title))
		ms += step
	}

	// Repeating passes.
	cycleDuration := repeatingCount * step
	for pass := range passes {
		if pass == 0 {
			// First cycle: one named chapter per frame.
			for i := range repeatingCount {
				title := fmt.Sprintf("Cycle 1, Min %d", i+1)
				if i < len(repeatingChapters) {
					title = repeatingChapters[i].Title
				}
				fmt.Fprintf(&sb, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n\n",
					ms, ms+step, escapeMetadata(title))
				ms += step
			}
		} else {
			// Subsequent cycles: one chapter for the whole cycle.
			fmt.Fprintf(&sb, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n\n",
				ms, ms+cycleDuration, escapeMetadata(fmt.Sprintf("Cycle %d", pass+1)))
			ms += cycleDuration
		}
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// escapeMetadata escapes special characters for the ffmetadata format.
func escapeMetadata(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "=", "\\=")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, "#", "\\#")
	s = strings.ReplaceAll(s, "\n", "\\\n")
	return s
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
