package connector

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
)

// ToXploraAMR transcodes any audio to AMR-NB (what Xplora watches expect).
// AMR-NB: 8 kHz, mono, ~7.95 kbps (mode 7). Max 60 s enforced by -t.
func ToXploraAMR(ctx context.Context, src []byte, srcMIME string) ([]byte, error) {
	srcFmt, err := mimeToFFmpegFormat(srcMIME)
	if err != nil {
		return nil, err
	}
	if _, lookupErr := exec.LookPath("ffmpeg"); lookupErr != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", lookupErr)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", srcFmt, "-i", "pipe:0",
		"-t", "60",
		"-ar", "8000", "-ac", "1",
		"-c:a", "libopencore_amrnb",
		"-b:a", "7950",
		"-f", "amr", "pipe:1",
	)
	cmd.Stdin = bytes.NewReader(src)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("audio→amr: %w\n%s", err, errBuf.String())
	}
	return out.Bytes(), nil
}

// FromXploraAMR transcodes AMR-NB audio (from the watch) to OGG Opus.
// Output: libopus, 48000 Hz, mono, 16 kbps — matches what Matrix clients expect.
func FromXploraAMR(ctx context.Context, src []byte) ([]byte, error) {
	if _, lookupErr := exec.LookPath("ffmpeg"); lookupErr != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", lookupErr)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "amr", "-i", "pipe:0",
		"-ar", "48000", "-ac", "1",
		"-c:a", "libopus",
		"-b:a", "16k",
		"-f", "ogg", "pipe:1",
	)
	cmd.Stdin = bytes.NewReader(src)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("amr→ogg: %w\n%s", err, errBuf.String())
	}
	return out.Bytes(), nil
}

// ExtractWaveformAndDuration decodes audio to raw S16LE PCM at 8 kHz mono,
// then computes both a 100-sample waveform (values 0–1023, per-bucket RMS
// normalised to peak) and the duration in milliseconds from the sample count.
// Returns empty slice and 0 on any error (non-fatal).
// Using PCM sample count for duration avoids a separate ffprobe call.
func ExtractWaveformAndDuration(ctx context.Context, data []byte, srcFormat string) ([]int, int) {
	if _, lookupErr := exec.LookPath("ffmpeg"); lookupErr != nil {
		return []int{}, 0
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", srcFormat, "-i", "pipe:0",
		"-ac", "1", "-ar", "8000",
		"-f", "s16le", "pipe:1",
	)
	cmd.Stdin = bytes.NewReader(data)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return []int{}, 0
	}
	pcm := out.Bytes()
	if len(pcm) < 200 {
		return []int{}, 0
	}

	samples := len(pcm) / 2
	durationMS := samples * 1000 / 8000 // exact: samples @ 8000 Hz

	const buckets = 100
	bucketSize := samples / buckets

	rms := make([]float64, buckets)
	var peak float64
	for i := 0; i < buckets; i++ {
		start := i * bucketSize
		end := start + bucketSize
		if end > samples {
			end = samples
		}
		var sum float64
		for j := start; j < end; j++ {
			v := float64(int16(binary.LittleEndian.Uint16(pcm[j*2 : j*2+2])))
			sum += v * v
		}
		rms[i] = math.Sqrt(sum / float64(end-start))
		if rms[i] > peak {
			peak = rms[i]
		}
	}
	if peak == 0 {
		return make([]int, buckets), durationMS
	}
	waveform := make([]int, buckets)
	for i, v := range rms {
		waveform[i] = int(v / peak * 1023)
	}
	return waveform, durationMS
}

// mimeToFFmpegFormat maps common audio MIME types to ffmpeg demuxer names.
func mimeToFFmpegFormat(mime string) (string, error) {
	switch mime {
	case "audio/ogg", "audio/ogg; codecs=vorbis", "audio/ogg; codecs=opus":
		return "ogg", nil
	case "audio/mpeg", "audio/mp3":
		return "mp3", nil
	case "audio/mp4", "audio/m4a", "audio/aac":
		return "aac", nil
	case "audio/wav", "audio/wave":
		return "wav", nil
	case "audio/webm":
		return "webm", nil
	case "audio/amr", "audio/amr-nb":
		return "amr", nil
	default:
		return "", fmt.Errorf("unsupported audio type: %s", mime)
	}
}
