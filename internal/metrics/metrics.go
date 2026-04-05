package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	registerOnce sync.Once
	inspectorMu  sync.RWMutex
	inspector    *asynq.Inspector

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_http_requests_total",
			Help: "Total number of HTTP requests handled by Vylux.",
		},
		[]string{"method", "route", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "vylux_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route", "status"},
	)

	imageCacheEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_image_cache_events_total",
			Help: "Image cache lookup results by cache layer.",
		},
		[]string{"layer", "result"},
	)

	imageResultsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_image_results_total",
			Help: "Image request processing results.",
		},
		[]string{"result"},
	)

	imageErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_image_errors_total",
			Help: "Image request failures grouped by processing stage and HTTP status.",
		},
		[]string{"stage", "status"},
	)

	workerTasksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_worker_tasks_total",
			Help: "Worker task attempts grouped by task type and result.",
		},
		[]string{"task_type", "result"},
	)

	workerTaskDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "vylux_worker_task_duration_seconds",
			Help:    "Worker task processing duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"task_type", "result"},
	)

	readinessFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_readiness_failures_total",
			Help: "Readiness probe failures grouped by dependency check.",
		},
		[]string{"check"},
	)

	queueTasks = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vylux_queue_tasks",
			Help: "Current number of tasks in each queue state.",
		},
		[]string{"queue", "state"},
	)

	queueMetricsSyncFailuresTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "vylux_queue_metrics_sync_failures_total",
			Help: "Number of queue depth sync failures while serving /metrics.",
		},
	)

	storageOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vylux_storage_operations_total",
			Help: "Storage operations grouped by role, driver, method, and result.",
		},
		[]string{"role", "driver", "operation", "result"},
	)

	storageOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "vylux_storage_operation_duration_seconds",
			Help:    "Storage operation latency grouped by role, driver, method, and result.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"role", "driver", "operation", "result"},
	)
)

func ensureRegistered() {
	registerOnce.Do(func() {
		prometheus.MustRegister(
			httpRequestsTotal,
			httpRequestDuration,
			imageCacheEventsTotal,
			imageResultsTotal,
			imageErrorsTotal,
			workerTasksTotal,
			workerTaskDuration,
			storageOperationsTotal,
			storageOperationDuration,
			readinessFailuresTotal,
			queueTasks,
			queueMetricsSyncFailuresTotal,
		)
	})
}

// ConfigureInspector registers the queue inspector used to refresh queue depth metrics.
func ConfigureInspector(i *asynq.Inspector) {
	inspectorMu.Lock()
	defer inspectorMu.Unlock()
	inspector = i
}

// Handler serves Prometheus metrics using the default registry.
func Handler(c *echo.Context) error {
	HTTPHandler().ServeHTTP(c.Response(), c.Request())
	return nil
}

// HTTPMiddleware records request counts and latencies for every Echo route.
func HTTPMiddleware() echo.MiddlewareFunc {
	ensureRegistered()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			started := time.Now()
			err := next(c)

			statusCode := responseStatus(c, err)
			status := strconv.Itoa(statusCode)
			route := c.Path()
			if route == "" {
				route = "unmatched"
			}

			labels := []string{c.Request().Method, route, status}
			httpRequestsTotal.WithLabelValues(labels...).Inc()
			httpRequestDuration.WithLabelValues(labels...).Observe(time.Since(started).Seconds())

			return err
		}
	}
}

// ObserveImageCache records a cache hit or miss for a specific image cache layer.
func ObserveImageCache(layer, result string) {
	ensureRegistered()
	imageCacheEventsTotal.WithLabelValues(layer, result).Inc()
}

// ObserveImageResult records the top-level outcome of an image request.
func ObserveImageResult(result string) {
	ensureRegistered()
	imageResultsTotal.WithLabelValues(result).Inc()
}

// ObserveImageError records that an image request failed.
func ObserveImageError(stage string, statusCode int) {
	ensureRegistered()
	imageErrorsTotal.WithLabelValues(stage, strconv.Itoa(statusCode)).Inc()
}

// ObserveWorkerTask records a worker task attempt and its duration.
func ObserveWorkerTask(taskType, result string, duration time.Duration) {
	ensureRegistered()
	workerTasksTotal.WithLabelValues(taskType, result).Inc()
	workerTaskDuration.WithLabelValues(taskType, result).Observe(duration.Seconds())
}

// ObserveStorageOperation records a storage API call and its duration.
func ObserveStorageOperation(role, driver, operation, result string, duration time.Duration) {
	ensureRegistered()
	storageOperationsTotal.WithLabelValues(role, driver, operation, result).Inc()
	storageOperationDuration.WithLabelValues(role, driver, operation, result).Observe(duration.Seconds())
}

// ObserveReadinessFailure records a readiness probe failure for a dependency check.
func ObserveReadinessFailure(check string) {
	ensureRegistered()
	readinessFailuresTotal.WithLabelValues(check).Inc()
}

func syncQueueDepth() {
	inspectorMu.RLock()
	i := inspector
	inspectorMu.RUnlock()
	if i == nil {
		return
	}

	queues, err := i.Queues()
	if err != nil {
		queueMetricsSyncFailuresTotal.Inc()
		return
	}

	for _, queueName := range queues {
		info, err := i.GetQueueInfo(queueName)
		if err != nil {
			queueMetricsSyncFailuresTotal.Inc()
			continue
		}

		queueTasks.WithLabelValues(queueName, "size").Set(float64(info.Size))
		queueTasks.WithLabelValues(queueName, "pending").Set(float64(info.Pending))
		queueTasks.WithLabelValues(queueName, "active").Set(float64(info.Active))
		queueTasks.WithLabelValues(queueName, "scheduled").Set(float64(info.Scheduled))
		queueTasks.WithLabelValues(queueName, "retry").Set(float64(info.Retry))
		queueTasks.WithLabelValues(queueName, "archived").Set(float64(info.Archived))
		queueTasks.WithLabelValues(queueName, "completed").Set(float64(info.Completed))
		queueTasks.WithLabelValues(queueName, "aggregating").Set(float64(info.Aggregating))
	}
}

func responseStatus(c *echo.Context, err error) int {
	_, status := echo.ResolveResponseStatus(c.Response(), err)
	if status > 0 {
		return status
	}

	return http.StatusOK
}
