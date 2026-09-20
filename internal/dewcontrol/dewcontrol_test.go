package dewcontrol

import "testing"

func TestComputeHeaterPercent(t *testing.T) {
	cases := []struct {
		name                      string
		temperature, dewPoint     float64
		deltaFullPower, deltaZero float64
		want                      int
	}{
		{"at or below full-power threshold -> 100%", 10, 9.5, 1.0, 5.0, 100},
		{"exactly at full-power threshold -> 100%", 10, 9.0, 1.0, 5.0, 100},
		{"at or above zero-power threshold -> 0%", 20, 10, 1.0, 5.0, 0},
		{"exactly at zero-power threshold -> 0%", 15, 10, 1.0, 5.0, 0},
		{"midpoint rounds to nearest 10", 13, 10, 1.0, 5.0, 50}, // delta=3, fraction=(5-3)/(5-1)=0.5 -> 50
		{"delta below zero (below dew point) still clamps to 100%", 8, 9, 1.0, 5.0, 100},
		{"rounds to nearest step, not just floor", 12, 10, 1.0, 5.0, 80}, // delta=2, fraction=0.75 -> 75% -> rounds to 80
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeHeaterPercent(c.temperature, c.dewPoint, c.deltaFullPower, c.deltaZero)
			if got != c.want {
				t.Errorf("computeHeaterPercent(%.1f, %.1f, %.1f, %.1f) = %d, want %d",
					c.temperature, c.dewPoint, c.deltaFullPower, c.deltaZero, got, c.want)
			}
		})
	}
}

func TestCoverGateBlocksHeat(t *testing.T) {
	cases := []struct {
		name         string
		onlyWhenOpen bool
		coverState   int
		want         bool
	}{
		{"option off, closed -> never blocks", false, 1, false},
		{"option off, open -> never blocks", false, 3, false},
		{"option off, unknown -> never blocks", false, 4, false},
		{"option on, closed -> blocks", true, 1, true},
		{"option on, moving -> blocks", true, 2, true},
		{"option on, open -> does not block", true, 3, false},
		{"option on, unknown -> blocks", true, 4, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coverGateBlocksHeat(c.onlyWhenOpen, c.coverState)
			if got != c.want {
				t.Errorf("coverGateBlocksHeat(%v, %d) = %v, want %v", c.onlyWhenOpen, c.coverState, got, c.want)
			}
		})
	}
}

func TestComputeHeaterState(t *testing.T) {
	cases := []struct {
		name                  string
		temperature, dewPoint float64
		targetDelta, hyst     float64
		currentlyOn           bool
		want                  bool
	}{
		{"delta below on-threshold -> turns on", 10, 9, 3.0, 1.0, false, true},               // delta=1, onThreshold=2
		{"delta at on-threshold -> turns on (tie favors on)", 11, 9, 3.0, 1.0, false, true},  // delta=2, onThreshold=2
		{"delta above off-threshold -> turns off", 15, 9, 3.0, 1.0, true, false},             // delta=6, offThreshold=4
		{"delta at off-threshold -> turns off (tie)", 13, 9, 3.0, 1.0, true, false},          // delta=4, offThreshold=4
		{"delta inside deadband, currently on -> holds on", 12, 9, 3.0, 1.0, true, true},     // delta=3, between 2 and 4
		{"delta inside deadband, currently off -> holds off", 12, 9, 3.0, 1.0, false, false}, // delta=3, between 2 and 4
		{"zero hysteresis, exactly at target -> turns on (tie)", 12, 9, 3.0, 0, false, true}, // delta=3, onThreshold=offThreshold=3
		{"zero hysteresis, just above target -> turns off", 12.1, 9, 3.0, 0, true, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeHeaterState(c.temperature, c.dewPoint, c.targetDelta, c.hyst, c.currentlyOn)
			if got != c.want {
				t.Errorf("computeHeaterState(%.1f, %.1f, %.1f, %.1f, %v) = %v, want %v",
					c.temperature, c.dewPoint, c.targetDelta, c.hyst, c.currentlyOn, got, c.want)
			}
		})
	}
}

func TestComputeHeaterPercentMalformedThresholds(t *testing.T) {
	// deltaFullPower >= deltaZeroPower is a misconfiguration; falls back to a plain
	// on/off switch at deltaFullPower rather than an inverted/nonsensical ramp.
	if got := computeHeaterPercent(20, 9, 5.0, 1.0); got != 0 {
		t.Errorf("expected 0%% when delta (11.0) exceeds the malformed full-power threshold (5.0), got %d", got)
	}
	if got := computeHeaterPercent(10, 9, 5.0, 1.0); got != 100 {
		t.Errorf("expected 100%% when delta (1.0) is below the malformed full-power threshold (5.0), got %d", got)
	}
}
