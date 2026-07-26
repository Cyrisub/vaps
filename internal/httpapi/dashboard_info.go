package httpapi

import (
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"vaps/internal/version"
)

type dashboardInfoResponse struct {
	Version   string               `json:"version"`
	Channel   string               `json:"channel"`
	StartedAt time.Time            `json:"started_at"`
	UptimeS   float64              `json:"uptime_s"`
	Runtime   dashboardInfoRuntime `json:"runtime"`
	Process   dashboardInfoProcess `json:"process"`
	Listen    dashboardInfoListen  `json:"listen"`
	Paths     dashboardInfoPaths   `json:"paths"`
	Config    dashboardInfoConfig  `json:"config"`
	Memory    dashboardInfoMemory  `json:"memory"`
}

type dashboardInfoRuntime struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	NumCPU    int    `json:"num_cpu"`
	Compiler  string `json:"compiler"`
}

type dashboardInfoProcess struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
}

type dashboardInfoListen struct {
	Addr string `json:"addr"`
}

type dashboardInfoPaths struct {
	DataDir    string `json:"data_dir"`
	MetadataDB string `json:"metadata_db"`
	AuthDB     string `json:"auth_db"`
	UploadDB   string `json:"upload_db"`
	UploadDir  string `json:"upload_dir"`
	LogDir     string `json:"log_dir"`
}

type dashboardInfoConfig struct {
	CacheBytes            *int64 `json:"cache_bytes,omitempty"`
	CacheMaxObjectBytes   *int64 `json:"cache_max_object_bytes,omitempty"`
	AuthExpireTime        string `json:"auth_expire_time"`
	UploadDirectMaxBytes  int64  `json:"upload_direct_max_bytes"`
	UploadExpiration      string `json:"upload_expiration"`
	UploadCleanupInterval string `json:"upload_cleanup_interval"`
	StatusLogInterval     string `json:"status_log_interval"`
	LogRetentionDays      int    `json:"log_retention_days"`
}

type dashboardInfoMemory struct {
	Alloc uint64 `json:"alloc"`
	Sys   uint64 `json:"sys"`
}

func (h *Handler) infoDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, infoDashboardHTML)
}

func (h *Handler) dashboardInfo(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, h.collectDashboardInfo())
}

func (h *Handler) collectDashboardInfo() dashboardInfoResponse {
	started := h.opts.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	uptime := time.Since(started).Seconds()
	if uptime < 0 {
		uptime = 0
	}

	executable, _ := os.Executable()
	hostname, _ := os.Hostname()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	config := dashboardInfoConfig{
		AuthExpireTime:        h.opts.AuthExpireTime.String(),
		UploadDirectMaxBytes:  h.opts.UploadDirectMaxBytes,
		UploadExpiration:      h.opts.UploadExpiration.String(),
		UploadCleanupInterval: h.opts.UploadCleanupInterval.String(),
		StatusLogInterval:     h.opts.StatusLogInterval.String(),
		LogRetentionDays:      h.opts.LogRetentionDays,
	}
	if h.opts.CacheBytes > 0 {
		cacheBytes := h.opts.CacheBytes
		cacheMaxObjectBytes := h.opts.CacheMaxObjectBytes
		config.CacheBytes = &cacheBytes
		config.CacheMaxObjectBytes = &cacheMaxObjectBytes
	}

	return dashboardInfoResponse{
		Version:   version.Version,
		Channel:   version.Channel,
		StartedAt: started.UTC(),
		UptimeS:   uptime,
		Runtime: dashboardInfoRuntime{
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			GoVersion: runtime.Version(),
			NumCPU:    runtime.NumCPU(),
			Compiler:  runtime.Compiler,
		},
		Process: dashboardInfoProcess{
			PID:        os.Getpid(),
			Executable: executable,
			Hostname:   hostname,
		},
		Listen: dashboardInfoListen{Addr: h.opts.ListenAddr},
		Paths: dashboardInfoPaths{
			DataDir:    h.opts.DataDir,
			MetadataDB: h.opts.MetadataDB,
			AuthDB:     h.opts.AuthDB,
			UploadDB:   h.opts.UploadDB,
			UploadDir:  h.opts.UploadDir,
			LogDir:     h.opts.LogDir,
		},
		Config: config,
		Memory: dashboardInfoMemory{
			Alloc: mem.Alloc,
			Sys:   mem.Sys,
		},
	}
}
