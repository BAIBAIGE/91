package mediasim

import (
	"context"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"
)

func randomFrame(seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	frame := make([]byte, frameBytes)
	for i := range frame {
		frame[i] = byte(rng.Intn(256))
	}
	return frame
}

func noisyCopy(frame []byte, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	out := make([]byte, len(frame))
	for i, v := range frame {
		delta := rng.Intn(7) - 3
		next := int(v) + delta
		if next < 0 {
			next = 0
		}
		if next > 255 {
			next = 255
		}
		out[i] = byte(next)
	}
	return out
}

func flatFrame(value byte) []byte {
	frame := make([]byte, frameBytes)
	for i := range frame {
		frame[i] = value
	}
	return frame
}

func TestSSIMLuma(t *testing.T) {
	a := randomFrame(1)
	if got := ssimLuma(a, a); got < 0.999 {
		t.Fatalf("identical frames ssim = %f, want ~1", got)
	}
	b := randomFrame(2)
	if got := ssimLuma(a, b); got > 0.3 {
		t.Fatalf("unrelated frames ssim = %f, want low", got)
	}
	if got := ssimLuma(a, noisyCopy(a, 3)); got < 0.95 {
		t.Fatalf("noisy re-encode ssim = %f, want high", got)
	}
	if got := ssimLuma(nil, nil); got != 0 {
		t.Fatalf("empty frames ssim = %f, want 0", got)
	}
}

func TestInformativeFrame(t *testing.T) {
	if informativeFrame(flatFrame(0)) {
		t.Fatalf("black frame reported informative")
	}
	if informativeFrame(flatFrame(200)) {
		t.Fatalf("uniform bright frame reported informative")
	}
	if informativeFrame(nil) {
		t.Fatalf("nil frame reported informative")
	}
	if !informativeFrame(randomFrame(1)) {
		t.Fatalf("random frame reported uninformative")
	}
}

func TestCompareFrameSignatures(t *testing.T) {
	base := make([][]byte, 0, FrameSignatureMaxFrames)
	for i := 0; i < FrameSignatureMaxFrames; i++ {
		base = append(base, randomFrame(int64(i+10)))
	}
	reencoded := make([][]byte, 0, FrameSignatureMaxFrames)
	for i, f := range base {
		reencoded = append(reencoded, noisyCopy(f, int64(i+100)))
	}

	same := CompareFrameSignatures(&FrameSignature{Frames: base}, &FrameSignature{Frames: reencoded})
	if same.Comparisons != FrameSignatureMaxFrames {
		t.Fatalf("comparisons = %d, want %d", same.Comparisons, FrameSignatureMaxFrames)
	}
	if !same.IsContentDuplicate() {
		t.Fatalf("re-encoded signature not detected as duplicate: %+v", same)
	}

	other := make([][]byte, 0, FrameSignatureMaxFrames)
	for i := 0; i < FrameSignatureMaxFrames; i++ {
		other = append(other, randomFrame(int64(i+500)))
	}
	diff := CompareFrameSignatures(&FrameSignature{Frames: base}, &FrameSignature{Frames: other})
	if diff.IsContentDuplicate() || diff.IsContentNearMiss() {
		t.Fatalf("unrelated signatures matched: %+v", diff)
	}

	// nil 占位帧与低方差帧都必须跳过，不参与统计。
	withGaps := append([][]byte{nil, flatFrame(0)}, base[2:]...)
	gapCmp := CompareFrameSignatures(&FrameSignature{Frames: withGaps}, &FrameSignature{Frames: base})
	if gapCmp.Comparisons != FrameSignatureMaxFrames-2 {
		t.Fatalf("comparisons with gaps = %d, want %d", gapCmp.Comparisons, FrameSignatureMaxFrames-2)
	}
	if !gapCmp.IsContentDuplicate() {
		t.Fatalf("gap signature should still match: %+v", gapCmp)
	}

	// 全为低方差帧时不可判定为重复（黑场视频互相 SSIM≈1 的陷阱）。
	flats := make([][]byte, FrameSignatureMaxFrames)
	for i := range flats {
		flats[i] = flatFrame(0)
	}
	flatCmp := CompareFrameSignatures(&FrameSignature{Frames: flats}, &FrameSignature{Frames: flats})
	if flatCmp.Comparisons != 0 || flatCmp.IsContentDuplicate() {
		t.Fatalf("all-flat signatures must not match: %+v", flatCmp)
	}
}

func TestCompareFrameSignaturesCross(t *testing.T) {
	base := make([][]byte, 0, FrameSignatureMaxFrames)
	for i := 0; i < FrameSignatureMaxFrames; i++ {
		base = append(base, randomFrame(int64(i+10)))
	}
	// 模拟 teaser 兜底段错位：右侧第一段（3 帧）换成无关画面，其余帧循环移位。
	shifted := make([][]byte, 0, FrameSignatureMaxFrames)
	for i := 0; i < 3; i++ {
		shifted = append(shifted, randomFrame(int64(i+900)))
	}
	shifted = append(shifted, base[:FrameSignatureMaxFrames-3]...)

	aligned := CompareFrameSignatures(&FrameSignature{Frames: base}, &FrameSignature{Frames: shifted})
	if aligned.IsContentDuplicate() {
		t.Fatalf("misaligned pair unexpectedly matched aligned rule: %+v", aligned)
	}
	cross := CompareFrameSignaturesCross(&FrameSignature{Frames: base}, &FrameSignature{Frames: shifted})
	if !cross.IsContentDuplicate() {
		t.Fatalf("misaligned duplicate not caught by cross rule: %+v", cross)
	}

	other := make([][]byte, 0, FrameSignatureMaxFrames)
	for i := 0; i < FrameSignatureMaxFrames; i++ {
		other = append(other, randomFrame(int64(i+500)))
	}
	crossDiff := CompareFrameSignaturesCross(&FrameSignature{Frames: base}, &FrameSignature{Frames: other})
	if crossDiff.IsContentDuplicate() {
		t.Fatalf("unrelated pair matched cross rule: %+v", crossDiff)
	}

	// 帧数不足时不可判定。
	short := &FrameSignature{Frames: base[:ContentDuplicateCrossMinFrames-1]}
	if c := CompareFrameSignaturesCross(short, &FrameSignature{Frames: base}); c.IsContentDuplicate() {
		t.Fatalf("short signature matched cross rule: %+v", c)
	}
}

func TestContentDuplicateRuleBoundaries(t *testing.T) {
	cases := []struct {
		cmp      FrameSignatureComparison
		dup      bool
		nearMiss bool
	}{
		{FrameSignatureComparison{Comparisons: ContentDuplicateMinComparisons, MedianSSIM: ContentDuplicateSSIMThreshold}, true, false},
		{FrameSignatureComparison{Comparisons: ContentDuplicateMinComparisons, MedianSSIM: ContentDuplicateSSIMThreshold - 0.001}, false, true},
		{FrameSignatureComparison{Comparisons: ContentDuplicateMinComparisons, MedianSSIM: ContentDuplicateNearMissThreshold - 0.001}, false, false},
		{FrameSignatureComparison{Comparisons: ContentDuplicateMinComparisons - 1, MedianSSIM: 0.99}, false, false},
	}
	for i, tc := range cases {
		if got := tc.cmp.IsContentDuplicate(); got != tc.dup {
			t.Fatalf("case %d IsContentDuplicate = %v, want %v", i, got, tc.dup)
		}
		if got := tc.cmp.IsContentNearMiss(); got != tc.nearMiss {
			t.Fatalf("case %d IsContentNearMiss = %v, want %v", i, got, tc.nearMiss)
		}
	}
}

// TestExtractFrameSignatureConsistency 验证两条提取路径（teaser 1fps 与按
// 时间戳抽帧）在同一素材上产出可对齐的帧——导入时通道依赖这一点。
func TestExtractFrameSignatureConsistency(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	sample := filepath.Join(dir, "sample.mp4")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=13:size=320x240:rate=12",
		"-pix_fmt", "yuv420p", "-y", sample).CombinedOutput()
	if err != nil {
		t.Fatalf("generate sample: %v, %s", err, out)
	}

	ctx := context.Background()
	teaserSig, err := ExtractTeaserFrameSignature(ctx, "", sample)
	if err != nil {
		t.Fatalf("teaser signature: %v", err)
	}
	if len(teaserSig.Frames) != FrameSignatureMaxFrames {
		t.Fatalf("teaser frames = %d, want %d", len(teaserSig.Frames), FrameSignatureMaxFrames)
	}
	if teaserSig.InformativeFrames() < ContentDuplicateMinComparisons {
		t.Fatalf("informative frames = %d", teaserSig.InformativeFrames())
	}

	times := make([]float64, FrameSignatureMaxFrames)
	for i := range times {
		times[i] = float64(i)
	}
	timedSig, err := ExtractFrameSignatureAtTimes(ctx, "", sample, times)
	if err != nil {
		t.Fatalf("timed signature: %v", err)
	}
	cmp := CompareFrameSignatures(teaserSig, timedSig)
	if cmp.Comparisons < ContentDuplicateMinComparisons {
		t.Fatalf("comparisons = %d, want >= %d", cmp.Comparisons, ContentDuplicateMinComparisons)
	}
	if cmp.MedianSSIM < 0.9 {
		t.Fatalf("median ssim between extraction paths = %f, want >= 0.9 (%+v)", cmp.MedianSSIM, cmp)
	}
}

func TestExtractFrameSignatureAtTimesRejectsEmpty(t *testing.T) {
	if _, err := ExtractFrameSignatureAtTimes(context.Background(), "", "nope.mp4", nil); err == nil {
		t.Fatalf("expected error for empty times")
	}
}

func BenchmarkSSIMLuma(b *testing.B) {
	left := randomFrame(1)
	right := randomFrame(2)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ssimLuma(left, right)
	}
}
