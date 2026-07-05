package httpapi

import "time"

type Options struct {
	AuthExpireTime        time.Duration
	UploadDirectMaxBytes  int64
	UploadExpiration      time.Duration
	UploadCleanupInterval time.Duration
}

func defaultOptions() Options {
	return Options{
		AuthExpireTime:        30 * time.Minute,
		UploadDirectMaxBytes:  8 * 1024 * 1024,
		UploadExpiration:      24 * time.Hour,
		UploadCleanupInterval: time.Minute,
	}
}
