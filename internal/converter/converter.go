package converter

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// FrameInput pairs a PNG slide with an optional demo video path.
// VideoPath is injected into the first EMOM pass only; warmup and strength
// tiles always leave it empty.
type FrameInput struct {
	PNGPath   string
	VideoPath string // abs path inside container; empty = plain tile cycle
}

const defaultTimer        = 60
const defaultTotalMinutes = 60

type Converter struct {
	outputDir string
	logOutput io.Writer
}

// New creates a Converter. logOutput receives FFmpeg stdout/stderr; pass
// io.Discard to suppress it (default when nil).
func New(outputDir string, logOutput io.Writer) *Converter {
	if logOutput == nil {
		logOutput = io.Discard
	}
	return &Converter{outputDir: outputDir, logOutput: logOutput}
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

	if err := c.encodeCycle(inputPath, cycleFile, timerSeconds, 1); err != nil {
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
// fps controls the output frame rate; use 1 for static tile-only cycles,
// 30 when the cycle is part of a video-enhanced workout.
func (c *Converter) encodeCycle(inputPath, outputPath string, timerSeconds, fps int) error {
	log.Printf("encoding %ds cycle from %s", timerSeconds, filepath.Base(inputPath))

	timerExpr := fmt.Sprintf(
		"drawtext=fontfile=/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf:"+
			"fontsize=72:fontcolor=white:"+
			"box=1:boxcolor=black@0.6:boxborderw=10:"+
			"text='%%{eif\\:%d-t-1\\:d}':"+
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
		"-r", fmt.Sprintf("%d", fps),
		"-c:a", "aac",
		"-b:a", "64k",
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = c.logOutput
	cmd.Stderr = c.logOutput
	if err := cmd.Run(); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("cycle encode failed: %w", err)
	}
	return nil
}

// encodeCycleWithVideo produces a timerSeconds-long segment that:
//  1. Shows the highlighted tile for 3 seconds
//  2. Cuts to the demo video (scaled/padded to width×height, audio muted)
//  3. Cuts back to the tile for the remaining time
//
// The countdown timer overlays all three segments continuously.
// fps should match the fps used for plain cycles in the same workout.
func (c *Converter) encodeCycleWithVideo(tilePath, videoPath, outputPath string, timerSeconds, fps, width, height int) error {
	log.Printf("encoding %ds video cycle from %s + %s", timerSeconds, filepath.Base(tilePath), filepath.Base(videoPath))

	demoDur, err := videoDuration(videoPath)
	if err != nil {
		return fmt.Errorf("could not probe demo video %s: %w", filepath.Base(videoPath), err)
	}

	const preDur = 3.0
	// Leave at least 2s of post-tile so the final beep is visible.
	maxDemoDur := float64(timerSeconds) - preDur - 2.0
	if demoDur > maxDemoDur {
		demoDur = maxDemoDur
	}
	postDur := float64(timerSeconds) - preDur - demoDur
	postOffset := int(preDur + demoDur) // integer seconds elapsed before post-tile

	b1, b2, b3 := timerSeconds-3, timerSeconds-2, timerSeconds-1
	beepFilter := fmt.Sprintf(
		"aevalsrc=0.5*sin(2*PI*1000*t)*(between(mod(t\\,%d)\\,%d\\,%d.4)+between(mod(t\\,%d)\\,%d\\,%d.4)+between(mod(t\\,%d)\\,%d\\,%d.4)):s=44100:c=mono",
		timerSeconds, b1, b1,
		timerSeconds, b2, b2,
		timerSeconds, b3, b3,
	)

	// drawtext helper: timer expression offset by `offset` seconds into the minute
	dt := func(offset int) string {
		return fmt.Sprintf(
			"drawtext=fontfile=/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf:"+
				"fontsize=72:fontcolor=white:"+
				"box=1:boxcolor=black@0.6:boxborderw=10:"+
				"text='%%{eif\\:%d-%d-t-1\\:d}':"+
				"x=w-tw-30:y=h-th-30",
			timerSeconds, offset,
		)
	}

	W, H := width, height

	filterComplex := strings.Join([]string{
		// Tile source: loop PNG for full timerSeconds, apply fps, split into two copies
		fmt.Sprintf("[0:v]fps=%d,scale=%d:%d,split=2[tile_a][tile_b]", fps, W, H),

		// Pre-tile: 0 → 3s, timer starts at T-1
		fmt.Sprintf("[tile_a]trim=end=%.3f,setpts=PTS-STARTPTS,%s[pre_v]", preDur, dt(0)),

		// Demo video: scale/pad to WxH, trim to cappedDemoDur, timer offset by 3s
		fmt.Sprintf(
			"[1:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS,trim=duration=%.3f,%s[demo_v]",
			W, H, W, H, fps, demoDur, dt(int(preDur)),
		),

		// Post-tile: remaining seconds, timer offset by 3+demoDur
		fmt.Sprintf("[tile_b]trim=duration=%.3f,setpts=PTS-STARTPTS,%s[post_v]", postDur, dt(postOffset)),

		// Concat all three video segments; audio comes from the beep lavfi source
		"[pre_v][demo_v][post_v]concat=n=3:v=1:a=0[outv]",
	}, ";")

	codec := "libx264"
	if gpu := os.Getenv("GPU_CODEC"); gpu != "" {
		codec = gpu
	}

	args := []string{
		"-y",
		"-loop", "1", "-t", fmt.Sprintf("%d", timerSeconds), "-i", tilePath,
		"-i", videoPath,
		"-f", "lavfi", "-i", beepFilter,
		"-filter_complex", filterComplex,
		"-map", "[outv]",
		"-map", "2:a",
		"-t", fmt.Sprintf("%d", timerSeconds),
		"-c:v", codec,
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "64k",
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = c.logOutput
	cmd.Stderr = c.logOutput
	if err := cmd.Run(); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("video cycle encode failed: %w", err)
	}
	return nil
}

// ConvertFramesTo encodes each frame as a timerSeconds-long cycle, then
// concatenates the frame sequence repeatedly to fill totalMinutes.
//
// For frames that carry a VideoPath, the first EMOM pass shows a 3s tile
// highlight → demo video → remaining tile; subsequent passes use plain tiles.
// Warmup frames (the first warmupCount entries) are always plain.
//
// width and height are the output resolution, used to scale demo videos.
func (c *Converter) ConvertFramesTo(frameInputs []FrameInput, outputPath string, timerSeconds, totalMinutes, warmupCount, width, height int, warmupChapters, repeatingChapters []Chapter, onFrameEncoded func()) error {
	if len(frameInputs) == 0 {
		return fmt.Errorf("no input frames provided")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("skipping %s — output already exists", filepath.Base(outputPath))
		return nil
	}

	// Determine fps: use 30 if any repeating frame carries a demo video,
	// so all cycle files share identical stream parameters for stream-copy concat.
	fps := 1
	for _, fi := range frameInputs[warmupCount:] {
		if fi.VideoPath != "" {
			fps = 30
			break
		}
	}

	// cleanupFiles collects every temp cycle file to remove on exit.
	cleanupFiles := map[string]struct{}{}
	defer func() {
		for p := range cleanupFiles {
			os.Remove(p)
		}
	}()

	cycleBase := strings.TrimSuffix(outputPath, ".mp4")

	// Encode warmup cycles (always plain).
	var warmupCycles []string
	for i, fi := range frameInputs[:warmupCount] {
		cf := fmt.Sprintf("%s_wu%d.cycle.mp4", cycleBase, i)
		if err := c.encodeCycle(fi.PNGPath, cf, timerSeconds, fps); err != nil {
			return err
		}
		warmupCycles = append(warmupCycles, cf)
		cleanupFiles[cf] = struct{}{}
		if onFrameEncoded != nil {
			onFrameEncoded()
		}
	}

	// Encode repeating cycles — two variants per frame when a video is present:
	//   firstPassCycles: video-enhanced (or plain if no video)
	//   repeatCycles:    always plain
	var firstPassCycles []string
	var repeatCycles []string

	for i, fi := range frameInputs[warmupCount:] {
		plainCF := fmt.Sprintf("%s_r%d.cycle.mp4", cycleBase, i)
		if err := c.encodeCycle(fi.PNGPath, plainCF, timerSeconds, fps); err != nil {
			return err
		}
		cleanupFiles[plainCF] = struct{}{}
		repeatCycles = append(repeatCycles, plainCF)

		if fi.VideoPath != "" {
			videoCF := fmt.Sprintf("%s_r%d_v.cycle.mp4", cycleBase, i)
			if err := c.encodeCycleWithVideo(fi.PNGPath, fi.VideoPath, videoCF, timerSeconds, fps, width, height); err != nil {
				log.Printf("warning: video cycle failed for %s, falling back to plain: %v", filepath.Base(fi.VideoPath), err)
				firstPassCycles = append(firstPassCycles, plainCF)
			} else {
				cleanupFiles[videoCF] = struct{}{}
				firstPassCycles = append(firstPassCycles, videoCF)
			}
		} else {
			firstPassCycles = append(firstPassCycles, plainCF)
		}

		if onFrameEncoded != nil {
			onFrameEncoded()
		}
	}

	if warmupCount < 0 || warmupCount > len(frameInputs) {
		warmupCount = 0
	}

	totalSeconds := totalMinutes * 60
	repeatingDuration := len(repeatCycles) * timerSeconds
	if repeatingDuration == 0 {
		repeatingDuration = timerSeconds
	}
	passes := totalSeconds / repeatingDuration
	if passes < 1 {
		passes = 1
	}

	if err := c.loopFrames(warmupCycles, firstPassCycles, repeatCycles, outputPath, timerSeconds, passes, warmupChapters, repeatingChapters); err != nil {
		os.Remove(outputPath)
		return err
	}

	log.Printf("done: %s", outputPath)
	return nil
}

// loopFrames stream-copies warmupFiles once, then firstPassRepeating once (pass 0),
// then repeatRepeating for (passes-1) additional passes.
func (c *Converter) loopFrames(warmupFiles, firstPassRepeating, repeatRepeating []string, outputPath string, timerSeconds, passes int, warmupChapters, repeatingChapters []Chapter) error {
	log.Printf("concatenating warmup(%d) + first-pass(%d) + %d frames × %d more passes -> %s",
		len(warmupFiles), len(firstPassRepeating), len(repeatRepeating), passes-1, filepath.Base(outputPath))

	concatFile := outputPath + ".txt"
	defer os.Remove(concatFile)

	var sb strings.Builder
	for _, cf := range warmupFiles {
		fmt.Fprintf(&sb, "file '%s'\n", cf)
	}
	// Pass 0: video-enhanced first pass
	for _, cf := range firstPassRepeating {
		fmt.Fprintf(&sb, "file '%s'\n", cf)
	}
	// Passes 1+: plain repeating
	for i := 1; i < passes; i++ {
		for _, cf := range repeatRepeating {
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
		if err := writeChapterMetadata(metaFile, len(warmupFiles), len(repeatRepeating), timerSeconds, passes, warmupChapters, repeatingChapters); err != nil {
			log.Printf("warning: chapter metadata skipped: %v", err)
		} else {
			defer os.Remove(metaFile)
			args = append(args, "-i", metaFile, "-map", "0", "-map_metadata", "1")
		}
	}

	args = append(args, "-c", "copy", "-movflags", "+faststart", outputPath)

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = c.logOutput
	cmd.Stderr = c.logOutput
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("concat frames failed: %w", err)
	}
	return nil
}

// videoDuration returns the duration of the first video stream in seconds.
func videoDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var probe struct {
		Streams []struct {
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return 0, fmt.Errorf("ffprobe parse: %w", err)
	}
	for _, s := range probe.Streams {
		if s.Duration != "" && s.Duration != "N/A" {
			d, err := strconv.ParseFloat(s.Duration, 64)
			if err != nil {
				return 0, fmt.Errorf("parse duration %q: %w", s.Duration, err)
			}
			return d, nil
		}
	}
	return 0, fmt.Errorf("no duration found in %s", filepath.Base(path))
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
	cmd.Stdout = c.logOutput
	cmd.Stderr = c.logOutput
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("concat loop failed: %w", err)
	}
	return nil
}
