package httpmeter

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const maxLatencySamples = 2048

type Sample struct {
	Status   int
	Duration time.Duration
	BytesIn  int64
	BytesOut int64
}

type RequestStats struct {
	Count    int64   `json:"count"`
	Active   int64   `json:"active"`
	Rate1    float64 `json:"rate_1"`
	RateMean float64 `json:"rate_mean"`
	TMin     float64 `json:"t_min"`
	TAvg     float64 `json:"t_avg"`
	TMax     float64 `json:"t_max"`
	TP50     float64 `json:"t_p50"`
	TP95     float64 `json:"t_p95"`
	TP99     float64 `json:"t_p99"`
}

type StatusStats struct {
	OK          int64 `json:"ok"`
	ClientError int64 `json:"client_error"`
	ServerError int64 `json:"server_error"`
	Other       int64 `json:"other"`
}

type ByteStats struct {
	In  int64 `json:"in"`
	Out int64 `json:"out"`
}

type Stats struct {
	Requests RequestStats `json:"requests"`
	Status   StatusStats  `json:"status"`
	Bytes    ByteStats    `json:"bytes"`
	UptimeS  float64      `json:"uptime_s"`
}

type Meter struct {
	startedAt time.Time

	active   atomic.Int64
	count    atomic.Int64
	ok       atomic.Int64
	client   atomic.Int64
	server   atomic.Int64
	other    atomic.Int64
	bytesIn  atomic.Int64
	bytesOut atomic.Int64

	mu        sync.Mutex
	latencies []float64
	window    []time.Time
}

func New() *Meter {
	return &Meter{
		startedAt: time.Now(),
		latencies: make([]float64, 0, 256),
		window:    make([]time.Time, 0, 128),
	}
}

func (m *Meter) Begin() func(Sample) {
	if m == nil {
		return func(Sample) {}
	}
	m.active.Add(1)
	return func(sample Sample) {
		m.finish(sample)
	}
}

func (m *Meter) finish(sample Sample) {
	m.active.Add(-1)
	m.count.Add(1)
	m.bytesIn.Add(sample.BytesIn)
	m.bytesOut.Add(sample.BytesOut)

	switch {
	case sample.Status >= 500:
		m.server.Add(1)
	case sample.Status >= 400:
		m.client.Add(1)
	case sample.Status >= 200 && sample.Status < 400:
		m.ok.Add(1)
	default:
		m.other.Add(1)
	}

	seconds := sample.Duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.latencies = append(m.latencies, seconds)
	if len(m.latencies) > maxLatencySamples {
		m.latencies = append([]float64(nil), m.latencies[len(m.latencies)-maxLatencySamples:]...)
	}
	m.window = append(m.window, now)
	cutoff := now.Add(-time.Minute)
	i := 0
	for i < len(m.window) && m.window[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		m.window = append([]time.Time(nil), m.window[i:]...)
	}
}

func (m *Meter) Stats() Stats {
	if m == nil {
		return Stats{}
	}
	count := m.count.Load()
	uptime := time.Since(m.startedAt).Seconds()
	if uptime <= 0 {
		uptime = 1
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rate1 := float64(len(m.window)) / 60.0
	stats := Stats{
		Requests: RequestStats{
			Count:    count,
			Active:   m.active.Load(),
			Rate1:    rate1,
			RateMean: float64(count) / uptime,
		},
		Status: StatusStats{
			OK:          m.ok.Load(),
			ClientError: m.client.Load(),
			ServerError: m.server.Load(),
			Other:       m.other.Load(),
		},
		Bytes: ByteStats{
			In:  m.bytesIn.Load(),
			Out: m.bytesOut.Load(),
		},
		UptimeS: uptime,
	}
	if len(m.latencies) == 0 {
		return stats
	}

	sorted := append([]float64(nil), m.latencies...)
	sort.Float64s(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	stats.Requests.TMin = sorted[0]
	stats.Requests.TMax = sorted[len(sorted)-1]
	stats.Requests.TAvg = sum / float64(len(sorted))
	stats.Requests.TP50 = percentile(sorted, 0.50)
	stats.Requests.TP95 = percentile(sorted, 0.95)
	stats.Requests.TP99 = percentile(sorted, 0.99)
	return stats
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	weight := rank - float64(lo)
	return sorted[lo]*(1-weight) + sorted[hi]*weight
}
