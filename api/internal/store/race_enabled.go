//go:build race

package store

const raceDetector = true

// bytes.growSlice also allocates a temporary slice in race builds.
const metadataJSONBufferGrowthMultiplier = 8
