package subtitles

import "testing"

func TestDurationCompatible(t *testing.T) {
	tests := []struct {
		name             string
		videoDuration    int
		subtitleDuration int
		want             bool
	}{
		{name: "unknown video duration", videoDuration: 0, subtitleDuration: 900, want: true},
		{name: "unknown subtitle duration", videoDuration: 900, subtitleDuration: 0, want: true},
		{name: "minimum tolerance boundary", videoDuration: 300, subtitleDuration: 330, want: true},
		{name: "outside minimum tolerance", videoDuration: 300, subtitleDuration: 331, want: false},
		{name: "percentage tolerance boundary", videoDuration: 3600, subtitleDuration: 3672, want: true},
		{name: "outside percentage tolerance", videoDuration: 3600, subtitleDuration: 3673, want: false},
		{name: "maximum tolerance boundary", videoDuration: 10000, subtitleDuration: 10120, want: true},
		{name: "outside maximum tolerance", videoDuration: 10000, subtitleDuration: 10121, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DurationCompatible(tt.videoDuration, tt.subtitleDuration); got != tt.want {
				t.Fatalf(
					"DurationCompatible(%d, %d) = %v, want %v",
					tt.videoDuration,
					tt.subtitleDuration,
					got,
					tt.want,
				)
			}
		})
	}
}
