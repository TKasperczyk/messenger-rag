package chunking

import "testing"

func TestHasTopicMarker(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "English_btw", text: "BTW, quick question", want: true},
		{name: "Leading_punct_and_space", text: "  (btw) quick question", want: true},
		{name: "Polish_swoja_droga_diacritic", text: "Swoją drogą, co u Ciebie?", want: true},
		{name: "French_a_propos_diacritic", text: "à propos: jeszcze jedno", want: true},
		{name: "Not_at_start", text: "Well btw, this is later", want: false},
		{name: "Prefix_of_longer_word", text: "btwxyz not a marker", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasTopicMarker(tt.text); got != tt.want {
				t.Fatalf("HasTopicMarker(%q)=%v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
