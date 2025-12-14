package chunking

import "testing"

func TestCountUniqueWordsUnicode(t *testing.T) {
	// Polish words with diacritics should be treated as a single word (not split).
	text := "Swoją drogą, wracając do tematu"
	if got, want := CountUniqueWords(text), 4; got != want {
		t.Fatalf("CountUniqueWords(%q)=%d, want %d", text, got, want)
	}
}
