package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppPort                  string
	AppMode                  string
	StateDBPath              string
	CPABaseURL               string
	CPAManagementKey         string
	CPAMBaseURL              string
	WebSessionSecret         string
	IssueScanIntervalSeconds int
	MaxPollConcurrency       int
	PriorityOrder            string
	EnableLogSignal          bool
	EnableQuotaPoll          bool
	RequestTimeoutSeconds    int
	EnableAutoIssueScan        bool
	EnableAutoReorder          bool
	AutoReorderIntervalSeconds int
	ProbeCooldownSeconds       int
	ProbeMaxPerRun             int
}

func Load() Config {
	return Config{
		AppPort:                  get("APP_PORT", "18417"),
		AppMode:                  get("APP_MODE", "dry-run"),
		StateDBPath:              get("STATE_DB_PATH", "/data/state.db"),
		CPABaseURL:               strings.TrimRight(get("CPA_BASE_URL", "http://host.docker.internal:8317"), "/"),
		CPAManagementKey:         get("CPA_MANAGEMENT_KEY", ""),
		CPAMBaseURL:              strings.TrimRight(get("CPAM_BASE_URL", "http://host.docker.internal:18317"), "/"),
		WebSessionSecret:         get("WEB_SESSION_SECRET", get("CPA_MANAGEMENT_KEY", "")),
		IssueScanIntervalSeconds: getInt("ISSUE_SCAN_INTERVAL_SECONDS", 60),
		MaxPollConcurrency:       getInt("MAX_POLL_CONCURRENCY", 2),
		PriorityOrder:            strings.ToLower(get("PRIORITY_ORDER", "desc")),
		EnableLogSignal:          getBool("ENABLE_LOG_SIGNAL", true),
		EnableQuotaPoll:          getBool("ENABLE_QUOTA_POLL", true),
		RequestTimeoutSeconds:    getInt("REQUEST_TIMEOUT_SECONDS", 15),
		EnableAutoIssueScan:        getBool("ENABLE_AUTO_ISSUE_SCAN", true),
		EnableAutoReorder:          getBool("ENABLE_AUTO_REORDER", false),
		AutoReorderIntervalSeconds: getInt("AUTO_REORDER_INTERVAL_SECONDS", 600),
		ProbeCooldownSeconds:       getInt("PROBE_COOLDOWN_SECONDS", 600),
		ProbeMaxPerRun:             getInt("PROBE_MAX_PER_RUN", 5),
	}
}

func get(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
