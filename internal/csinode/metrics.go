package csinode

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

var nodeMetrics struct {
	publishTotal        atomic.Uint64
	unpublishTotal      atomic.Uint64
	snapshotTotal       atomic.Uint64
	snapshotErrorsTotal atomic.Uint64
	checkpointTotal     atomic.Uint64
}

func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE osix_csi_publish_total counter\nosix_csi_publish_total %d\n", nodeMetrics.publishTotal.Load())
		fmt.Fprintf(w, "# TYPE osix_csi_unpublish_total counter\nosix_csi_unpublish_total %d\n", nodeMetrics.unpublishTotal.Load())
		fmt.Fprintf(w, "# TYPE osix_csi_snapshot_total counter\nosix_csi_snapshot_total %d\n", nodeMetrics.snapshotTotal.Load())
		fmt.Fprintf(w, "# TYPE osix_csi_snapshot_errors_total counter\nosix_csi_snapshot_errors_total %d\n", nodeMetrics.snapshotErrorsTotal.Load())
		fmt.Fprintf(w, "# TYPE osix_csi_checkpoint_total counter\nosix_csi_checkpoint_total %d\n", nodeMetrics.checkpointTotal.Load())
	})
}
