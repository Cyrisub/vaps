package httpmeter_test

import (
	"testing"
	"time"

	"vaps/internal/httpmeter"
)

func TestMeterTracksActiveTotalRateAndLatency(t *testing.T) {
	meter := httpmeter.New()

	done1 := meter.Begin()
	done2 := meter.Begin()
	stats := meter.Stats()
	if stats.Requests.Active != 2 {
		t.Fatalf("active = %d, want 2", stats.Requests.Active)
	}
	if stats.Requests.Count != 0 {
		t.Fatalf("count before finish = %d, want 0", stats.Requests.Count)
	}

	done1(httpmeter.Sample{Status: 200, Duration: 100 * time.Millisecond, BytesIn: 10, BytesOut: 20})
	done2(httpmeter.Sample{Status: 404, Duration: 200 * time.Millisecond, BytesIn: 5, BytesOut: 8})
	time.Sleep(10 * time.Millisecond)

	stats = meter.Stats()
	if stats.Requests.Active != 0 {
		t.Fatalf("active after finish = %d, want 0", stats.Requests.Active)
	}
	if stats.Requests.Count != 2 {
		t.Fatalf("count = %d, want 2", stats.Requests.Count)
	}
	if stats.Requests.RateMean <= 0 {
		t.Fatalf("rate_mean = %v, want > 0", stats.Requests.RateMean)
	}
	if stats.Requests.TAvg <= 0 || stats.Requests.TMin <= 0 || stats.Requests.TMax <= 0 {
		t.Fatalf("latency stats missing: %#v", stats.Requests)
	}
	if stats.Requests.TP50 <= 0 || stats.Requests.TP95 <= 0 || stats.Requests.TP99 <= 0 {
		t.Fatalf("percentile stats missing: %#v", stats.Requests)
	}
	if stats.Status.OK != 1 || stats.Status.ClientError != 1 {
		t.Fatalf("status counts = %#v", stats.Status)
	}
	if stats.Bytes.In != 15 || stats.Bytes.Out != 28 {
		t.Fatalf("bytes = %#v", stats.Bytes)
	}
}

func TestMeterIgnoresNilReceiver(t *testing.T) {
	var meter *httpmeter.Meter
	done := meter.Begin()
	done(httpmeter.Sample{Status: 200, Duration: time.Millisecond})
	if stats := meter.Stats(); stats.Requests.Count != 0 {
		t.Fatalf("nil meter stats = %#v", stats)
	}
}
