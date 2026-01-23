package memory

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// observationsStored counts the total number of observations stored
	observationsStored = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "eve_memory_observations_stored_total",
			Help: "Total number of observations stored",
		},
		[]string{"type", "channel_id"},
	)

	// searchRequests counts the total number of search requests
	searchRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "eve_memory_search_requests_total",
			Help: "Total number of search requests",
		},
		[]string{"channel_id"},
	)

	// searchLatency measures search latency
	searchLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "eve_memory_search_latency_seconds",
			Help:    "Search latency in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		},
		[]string{"channel_id"},
	)

	// embedLatency measures embedding generation latency
	embedLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "eve_memory_embed_latency_seconds",
			Help:    "Embedding generation latency in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
	)

	// qdrantHealthy indicates Qdrant health status
	qdrantHealthy = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "eve_memory_qdrant_healthy",
			Help: "Qdrant health status (1 = healthy, 0 = unhealthy)",
		},
	)

	// sqliteHealthy indicates SQLite health status
	sqliteHealthy = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "eve_memory_sqlite_healthy",
			Help: "SQLite health status (1 = healthy, 0 = unhealthy)",
		},
	)

	// sessionsActive counts active sessions
	sessionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "eve_memory_sessions_active",
			Help: "Number of active sessions",
		},
	)

	// observationsTotal is the total count of observations
	observationsTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "eve_memory_observations_total",
			Help: "Total number of observations in store",
		},
	)
)

// RecordObservationStored records a stored observation metric
func RecordObservationStored(obsType, channelID string) {
	observationsStored.WithLabelValues(obsType, channelID).Inc()
}

// RecordSearchRequest records a search request metric
func RecordSearchRequest(channelID string) {
	searchRequests.WithLabelValues(channelID).Inc()
}

// ObserveSearchLatency records search latency
func ObserveSearchLatency(channelID string, seconds float64) {
	searchLatency.WithLabelValues(channelID).Observe(seconds)
}

// ObserveEmbedLatency records embedding latency
func ObserveEmbedLatency(seconds float64) {
	embedLatency.Observe(seconds)
}

// SetQdrantHealthy sets the Qdrant health status
func SetQdrantHealthy(healthy bool) {
	if healthy {
		qdrantHealthy.Set(1)
	} else {
		qdrantHealthy.Set(0)
	}
}

// SetSQLiteHealthy sets the SQLite health status
func SetSQLiteHealthy(healthy bool) {
	if healthy {
		sqliteHealthy.Set(1)
	} else {
		sqliteHealthy.Set(0)
	}
}

// SetActiveSessionsCount sets the active sessions count
func SetActiveSessionsCount(count int) {
	sessionsActive.Set(float64(count))
}

// SetTotalObservationsCount sets the total observations count
func SetTotalObservationsCount(count int64) {
	observationsTotal.Set(float64(count))
}
