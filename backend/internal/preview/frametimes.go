package preview

// TeaserFrameSourceTimes 把 1fps 提取的 teaser 帧序号映射回源视频时间戳。
// 选段方案只由时长决定，因此该映射与实际生成 teaser 时使用的主选段一致；
// 若当时某段回退到了备选起点，对应帧只会比不上而不会误对齐。
func TeaserFrameSourceTimes(duration float64, frameCount int) []float64 {
	plan := buildTeaserPlan(Config{}, duration)
	if len(plan.starts) == 0 || plan.eachSec <= 0 || frameCount <= 0 {
		return nil
	}
	out := make([]float64, 0, frameCount)
	for k := 0; k < frameCount; k++ {
		seg := int(float64(k) / plan.eachSec)
		if seg >= len(plan.starts) {
			break
		}
		out = append(out, plan.starts[seg]+float64(k)-float64(seg)*plan.eachSec)
	}
	return out
}
