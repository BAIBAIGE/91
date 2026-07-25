package subtitles

import "context"

// Request contains only catalog metadata that is safe to send to an anonymous
// subtitle lookup service. It is deliberately independent from drive types and
// credentials.
type Request struct {
	FileID   string
	FileName string
	// LookupNames contains preferred semantic names, such as an AV code
	// extracted from a noisy release filename.
	LookupNames     []string
	ContentHash     string
	SampledSHA256   string
	DurationSeconds int
}

// Subtitle is an online subtitle candidate returned by a Client.
type Subtitle struct {
	ID              string
	Name            string
	Ext             string
	Language        string
	URL             string
	Source          int
	SourceLabel     string
	DurationSeconds int
}

// Client fetches online subtitle candidates without depending on a mounted
// storage drive.
type Client interface {
	Subtitles(ctx context.Context, req Request) ([]Subtitle, error)
}

// DurationCompatible reports whether a subtitle's known duration is close
// enough to the video's duration to be a usable candidate. An unknown duration
// on either side is retained because it cannot be disproved.
func DurationCompatible(videoDuration, subtitleDuration int) bool {
	if videoDuration <= 0 || subtitleDuration <= 0 {
		return true
	}
	tolerance := int(float64(videoDuration) * 0.02)
	if tolerance < 30 {
		tolerance = 30
	}
	if tolerance > 120 {
		tolerance = 120
	}
	delta := subtitleDuration - videoDuration
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}
