//go:build !race

package secretscan

// raceSlowdown scales timing limits under the race detector.
const raceSlowdown = 1
