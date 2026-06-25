package k8soperator

import (
	"errors"
	"testing"
)

func TestStatusHelpersAndMetrics(t *testing.T) {
	status := AgentOCIStatus{}
	MarkMounted(&status, "main", "published")
	if !hasCondition(status, ConditionMounted, "True") || !hasCondition(status, ConditionReady, "True") {
		t.Fatalf("mounted conditions missing: %#v", status.Conditions)
	}

	MarkSnapshotResult(&status, "sha256:snap", "sha256:checkpoint", nil)
	if status.LastSnapshotDigest != "sha256:snap" || status.LastCheckpointDigest != "sha256:checkpoint" || status.LastError != "" {
		t.Fatalf("snapshot status mismatch: %#v", status)
	}
	if !hasCondition(status, ConditionCheckpointed, "True") || !hasCondition(status, ConditionSnapshotFailed, "False") {
		t.Fatalf("snapshot conditions missing: %#v", status.Conditions)
	}

	MarkSnapshotResult(&status, "", "", errors.New("registry denied push"))
	if status.LastError != "registry denied push" || !hasCondition(status, ConditionSnapshotFailed, "True") {
		t.Fatalf("failure condition missing: %#v", status)
	}

	for _, want := range []string{"osix_operator_reconcile_total", "osix_csi_snapshot_total", "osix_csi_checkpoint_total"} {
		if !hasMetric(want) {
			t.Fatalf("metric %q missing from %#v", want, MetricNames)
		}
	}
}

func hasCondition(status AgentOCIStatus, conditionType, conditionStatus string) bool {
	for _, condition := range status.Conditions {
		if condition.Type == conditionType && condition.Status == conditionStatus {
			return true
		}
	}
	return false
}

func hasMetric(name string) bool {
	for _, metric := range MetricNames {
		if metric == name {
			return true
		}
	}
	return false
}
