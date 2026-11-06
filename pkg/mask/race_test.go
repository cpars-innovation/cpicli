//go:build race

package mask

// raceSlowdown scales timing limits under the race detector.
const raceSlowdown = 10
