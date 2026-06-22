// Package pqi implements the Path Quality Index (PQI) used by the multipath
// scheduler to rank Wi-Fi / 5G paths and drive soft handover for autonomous
// mobile robots.
//
// The pipeline is: raw per-path metrics (RTT, loss, bandwidth) -> min-max
// normalization across the candidate paths -> weighted cost -> PQI = 100*(1-cost)
// -> EWMA temporal smoothing + sliding-window trend -> hysteresis handover
// decision. See docs/paper-revision/reviewer-response.md for the formal
// definition that mirrors this code.
package pqi

// MinMaxNormalize maps each value to [0,1] using (v - min) / (max - min) over the
// provided set, so the cost is scale-free across heterogeneous metrics.
//
// When every value is equal (max == min) the metric cannot differentiate the
// paths, so all outputs are 0 (neutral): the metric contributes nothing to the
// relative cost. The empty input returns an empty slice.
func MinMaxNormalize(values []float64) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 {
		return out
	}
	mn, mx := values[0], values[0]
	for _, v := range values {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	rng := mx - mn
	if rng == 0 {
		return out
	}
	for i, v := range values {
		out[i] = (v - mn) / rng
	}
	return out
}
