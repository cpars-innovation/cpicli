//go:build race

package scope

// raceSlowdown scales timing limits under the race detector.
const raceSlowdown = 20
