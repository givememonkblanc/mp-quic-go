package pqi

import "time"

// Metrics is a raw per-path measurement sample feeding the PQI.
//
//   - RTT:       smoothed round-trip time of the path (lower is better)
//   - LossRate:  packet loss fraction in [0,1] (lower is better)
//   - Bandwidth: estimated goodput in bytes/s (higher is better)
//
// RTT comes from the path's own QUIC sent-packet handler (per-path RTT), loss
// from the path's (PATH_)ACK feedback, and bandwidth from delivered bytes over
// elapsed time. RSSI is handled separately as a link-layer signal.
type Metrics struct {
	RTT       time.Duration
	LossRate  float64
	Bandwidth float64
}

// Weights are the cost-function coefficients (alpha for RTT, beta for loss,
// gamma for bandwidth). They should sum to 1.
type Weights struct {
	Alpha float64
	Beta  float64
	Gamma float64
}

// DefaultWeights weights RTT most, then loss, then bandwidth.
func DefaultWeights() Weights { return Weights{Alpha: 0.5, Beta: 0.3, Gamma: 0.2} }

// ComputePQI returns the raw PQI (0..100, higher is better) for each path in the
// input set. Metrics are min-max normalized across the set, combined into the
// weighted cost C = alpha*RTT~ + beta*loss~ + gamma*(1 - bw~), and reported as
// PQI = 100*(1 - C). With a single path the normalized metrics are all 0, giving
// PQI = 100*(1 - gamma) (no relative comparison is possible).
func ComputePQI(m []Metrics, w Weights) []float64 {
	n := len(m)
	rtt := make([]float64, n)
	loss := make([]float64, n)
	bw := make([]float64, n)
	for i := range m {
		rtt[i] = float64(m[i].RTT)
		loss[i] = m[i].LossRate
		bw[i] = m[i].Bandwidth
	}
	rn := MinMaxNormalize(rtt)
	ln := MinMaxNormalize(loss)
	bn := MinMaxNormalize(bw)
	out := make([]float64, n)
	for i := range m {
		cost := w.Alpha*rn[i] + w.Beta*ln[i] + w.Gamma*(1-bn[i])
		out[i] = 100 * (1 - cost)
	}
	return out
}

// Estimator applies EWMA temporal smoothing to a path's raw PQI and keeps a
// sliding window of recent smoothed values for trend detection.
type Estimator struct {
	lambda float64
	window int
	value  float64
	init   bool
	hist   []float64
}

// NewEstimator creates an estimator with EWMA factor lambda (0<lambda<=1; higher
// reacts faster) and a trend window of the given number of samples (min 2).
func NewEstimator(lambda float64, window int) *Estimator {
	if window < 2 {
		window = 2
	}
	if lambda <= 0 || lambda > 1 {
		lambda = 0.3
	}
	return &Estimator{lambda: lambda, window: window}
}

// Update folds a new raw PQI into the EWMA and returns the smoothed value.
func (e *Estimator) Update(raw float64) float64 {
	if !e.init {
		e.value = raw
		e.init = true
	} else {
		e.value = e.lambda*raw + (1-e.lambda)*e.value
	}
	e.hist = append(e.hist, e.value)
	if len(e.hist) > e.window {
		e.hist = e.hist[len(e.hist)-e.window:]
	}
	return e.value
}

// Value returns the current smoothed PQI.
func (e *Estimator) Value() float64 { return e.value }

// Trend returns the average per-sample change of the smoothed PQI over the
// window. A negative value means the path quality is degrading (used to trigger
// proactive, pre-failure handover), positive means recovering.
func (e *Estimator) Trend() float64 {
	if len(e.hist) < 2 {
		return 0
	}
	return (e.hist[len(e.hist)-1] - e.hist[0]) / float64(len(e.hist)-1)
}
