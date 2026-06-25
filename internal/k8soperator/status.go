package k8soperator

const (
	ConditionReady          = "Ready"
	ConditionMounted        = "Mounted"
	ConditionSnapshotting   = "Snapshotting"
	ConditionSnapshotFailed = "SnapshotFailed"
	ConditionCheckpointed   = "Checkpointed"
	ConditionRegistryReady  = "RegistryReady"
)

var MetricNames = []string{
	"osix_operator_reconcile_total",
	"osix_operator_reconcile_errors_total",
	"osix_csi_publish_total",
	"osix_csi_unpublish_total",
	"osix_csi_snapshot_total",
	"osix_csi_snapshot_errors_total",
	"osix_csi_checkpoint_total",
}

func MarkMounted(status *AgentOCIStatus, sourceRef, message string) {
	status.LastError = ""
	SetCondition(status, Condition{Type: ConditionMounted, Status: "True", Reason: "Published", Message: message})
	SetCondition(status, Condition{Type: ConditionReady, Status: "True", Reason: "Mounted", Message: sourceRef})
}

func MarkSnapshotResult(status *AgentOCIStatus, snapshotDigest, checkpointDigest string, err error) {
	if err != nil {
		status.LastError = err.Error()
		SetCondition(status, Condition{Type: ConditionSnapshotFailed, Status: "True", Reason: "SnapshotError", Message: err.Error()})
		SetCondition(status, Condition{Type: ConditionSnapshotting, Status: "False", Reason: "Failed"})
		return
	}
	status.LastError = ""
	status.LastSnapshotDigest = snapshotDigest
	status.LastCheckpointDigest = checkpointDigest
	SetCondition(status, Condition{Type: ConditionSnapshotFailed, Status: "False", Reason: "SnapshotSucceeded"})
	SetCondition(status, Condition{Type: ConditionSnapshotting, Status: "False", Reason: "Completed"})
	if checkpointDigest != "" {
		SetCondition(status, Condition{Type: ConditionCheckpointed, Status: "True", Reason: "CheckpointCreated", Message: checkpointDigest})
	}
}

func MarkRegistry(status *AgentOCIStatus, ready bool, reason, message string) {
	SetCondition(status, Condition{Type: ConditionRegistryReady, Status: ConditionStatus(ready), Reason: reason, Message: message})
}
