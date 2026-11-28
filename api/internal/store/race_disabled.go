//go:build !race

package store

const raceDetector = false

const metadataJSONBufferGrowthMultiplier = 4
