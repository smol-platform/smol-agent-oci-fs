#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"${repo_root}/scripts/test-registry-hosted-doctor.sh"
"${repo_root}/scripts/test-registry-hosted-setup.sh"
"${repo_root}/scripts/test-registry-hosted-probe.sh"
"${repo_root}/scripts/test-registry-hosted-probe-verifier.sh"
"${repo_root}/scripts/test-registry-hosted-runner.sh"
"${repo_root}/scripts/test-registry-hosted-run-verifier.sh"
"${repo_root}/scripts/test-registry-hosted-run-summary.sh"
"${repo_root}/scripts/test-registry-hosted-preflight.sh"
"${repo_root}/scripts/test-registry-hosted-matrix-report.sh"
"${repo_root}/scripts/test-registry-hosted-evidence.sh"
"${repo_root}/scripts/test-registry-hosted-compatibility-update.sh"

bash -n \
	"${repo_root}/scripts/doctor-hosted-registry.sh" \
	"${repo_root}/scripts/test-registry-hosted-doctor.sh" \
	"${repo_root}/scripts/probe-hosted-registry.sh" \
	"${repo_root}/scripts/test-registry-hosted-probe.sh" \
	"${repo_root}/scripts/verify-hosted-registry-probes.sh" \
	"${repo_root}/scripts/test-registry-hosted-probe-verifier.sh" \
	"${repo_root}/scripts/verify-hosted-registry-run.sh" \
	"${repo_root}/scripts/test-registry-hosted-run-verifier.sh" \
	"${repo_root}/scripts/summarize-hosted-registry-run.sh" \
	"${repo_root}/scripts/test-registry-hosted-run-summary.sh" \
	"${repo_root}/scripts/run-hosted-registry-verification.sh" \
	"${repo_root}/scripts/test-registry-hosted-runner.sh" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	"${repo_root}/scripts/test-registry-hosted-setup.sh" \
	"${repo_root}/scripts/update-hosted-registry-compatibility.sh" \
	"${repo_root}/scripts/test-registry-hosted-compatibility-update.sh" \
	"${repo_root}/scripts/verify-hosted-registry-evidence.sh" \
	"${repo_root}/scripts/test-registry-hosted-evidence.sh" \
	"${repo_root}/scripts/test-registry-hosted-preflight.sh" \
	"${repo_root}/scripts/test-registry-hosted-matrix-report.sh" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh" \
	"${repo_root}/scripts/test-registry-hosted.sh"

go test "${repo_root}/cmd/osix" "${repo_root}/internal/osix" -run 'TestRegistryProbeCommand|TestLoadDockerCredential.*|TestPushPullSnapshotThroughOCIRegistry'

echo "Hosted registry local suite passed"
