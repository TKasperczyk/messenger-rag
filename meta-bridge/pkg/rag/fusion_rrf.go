package rag

import "sort"

// sortHits sorts hits by RRF score with tiebreakers
// Order: RRF score desc -> present in both -> lower BM25 rank -> lower vector rank
func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]

		// Primary: RRF score descending
		aScore := float64(0)
		bScore := float64(0)
		if a.RrfScore != nil {
			aScore = *a.RrfScore
		}
		if b.RrfScore != nil {
			bScore = *b.RrfScore
		}
		if aScore != bScore {
			return aScore > bScore
		}

		// Tiebreaker 1: Prefer results present in both sources
		aHasBoth := a.VectorRank != nil && a.BM25Rank != nil
		bHasBoth := b.VectorRank != nil && b.BM25Rank != nil
		if aHasBoth && !bHasBoth {
			return true
		}
		if !aHasBoth && bHasBoth {
			return false
		}

		// Tiebreaker 2: Lower BM25 rank is better
		aBM25 := maxInt
		bBM25 := maxInt
		if a.BM25Rank != nil {
			aBM25 = *a.BM25Rank
		}
		if b.BM25Rank != nil {
			bBM25 = *b.BM25Rank
		}
		if aBM25 != bBM25 {
			return aBM25 < bBM25
		}

		// Tiebreaker 3: Lower vector rank is better
		aVec := maxInt
		bVec := maxInt
		if a.VectorRank != nil {
			aVec = *a.VectorRank
		}
		if b.VectorRank != nil {
			bVec = *b.VectorRank
		}
		if aVec != bVec {
			return aVec < bVec
		}

		// Final tiebreaker: chunk ID for stability
		return a.ChunkID < b.ChunkID
	})
}

const maxInt = 1<<31 - 1
