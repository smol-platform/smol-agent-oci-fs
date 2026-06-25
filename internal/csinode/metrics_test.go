package csinode

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandlerExposesCSICounters(t *testing.T) {
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		"osix_csi_publish_total",
		"osix_csi_unpublish_total",
		"osix_csi_snapshot_total",
		"osix_csi_snapshot_errors_total",
		"osix_csi_checkpoint_total",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q: %s", want, body)
		}
	}
}
