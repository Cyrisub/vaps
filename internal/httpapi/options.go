package httpapi

import "time"

type Options struct {
	AuthExpireTime        time.Duration
	UploadDirectMaxBytes  int64
	UploadExpiration      time.Duration
	UploadCleanupInterval time.Duration

	// Server info (dashboard Info page). Optional; defaults filled in NewV2.
	ListenAddr          string
	DataDir             string
	MetadataDB          string
	AuthDB              string
	UploadDB            string
	UploadDir           string
	LogDir              string
	LogRetentionDays    int
	StatusLogInterval   time.Duration
	CacheBytes          int64
	CacheMaxObjectBytes int64
	CosEndpoint         string
	CosRegion           string
	CosBucket           string
	CosPrefix           string
	InfoRefreshInterval time.Duration
	StartedAt           time.Time
}

func defaultOptions() Options {
	return Options{
		AuthExpireTime:        30 * time.Minute,
		UploadDirectMaxBytes:  8 * 1024 * 1024,
		UploadExpiration:      24 * time.Hour,
		UploadCleanupInterval: time.Minute,
		InfoRefreshInterval:   5 * time.Second,
	}
}
