#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

failed="${tmp}/failed"
mkdir -p "${failed}/probe" "${failed}/logs"
cat > "${failed}/run.json" <<JSON
{
  "exitCode": 1,
  "message": "hosted registry probe failed for ecr",
  "paths": {
    "logs": "${failed}/logs",
    "matrix": "${failed}/matrix.json",
    "preflight": "${failed}/preflight.json",
    "probe": "${failed}/probe"
  },
  "providers": ["ecr"],
  "result": "failed",
  "stage": "probe:ecr",
  "updateDocs": false
}
JSON
cat > "${failed}/preflight.json" <<JSON
{
  "matrix": ["ecr"],
  "providers": [
    {
      "missing": [],
      "provider": "ecr",
      "providerCLIIdentities": {
        "aws": {
          "authenticated": false,
          "available": true,
          "error": "Unable to locate credentials."
        }
      },
      "readyToRun": true,
      "repository": "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat"
    }
  ],
  "required": ["ecr"]
}
JSON
cat > "${failed}/probe/ecr-20260622T225015Z.json" <<JSON
{
  "exitCode": 1,
  "failureClass": "registry-write-forbidden",
  "failureHint": "ecr registry rejected blob upload with 403 Forbidden; refresh credentials or grant repository write/push permissions.",
  "logFile": "${failed}/probe/ecr-20260622T225015Z.log",
  "profile": "ecr",
  "provider": "ecr",
  "registryHost": "123456789012.dkr.ecr.us-east-1.amazonaws.com",
  "repository": "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat",
  "result": "failed",
  "tag": "osix-probe-ecr",
  "testedAt": "2026-06-22T22:50:15Z"
}
JSON
printf '403 Forbidden\n' > "${failed}/logs/ecr-probe.log"

"${repo_root}/scripts/summarize-hosted-registry-run.sh" "${failed}" > "${tmp}/failed.out"
grep -q "Result: failed" "${tmp}/failed.out"
grep -q "Stage: probe:ecr" "${tmp}/failed.out"
grep -q "failure: registry-write-forbidden" "${tmp}/failed.out"
grep -q "next: Grant ECR push permissions" "${tmp}/failed.out"
grep -q "aws ecr describe-repositories --region us-east-1 --repository-names osix-compat" "${tmp}/failed.out"
grep -q "aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 123456789012.dkr.ecr.us-east-1.amazonaws.com" "${tmp}/failed.out"
grep -q "OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat ./scripts/run-hosted-registry-verification.sh --providers ecr" "${tmp}/failed.out"
grep -q "matrix: not-run" "${tmp}/failed.out"

passed="${tmp}/passed"
mkdir -p "${passed}/probe" "${passed}/logs"
cat > "${passed}/run.json" <<JSON
{
  "exitCode": 0,
  "message": "hosted registry verification complete",
  "paths": {
    "logs": "${passed}/logs",
    "matrix": "${passed}/matrix.json",
    "preflight": "${passed}/preflight.json",
    "probe": "${passed}/probe"
  },
  "providers": ["gar"],
  "result": "passed",
  "stage": "complete",
  "updateDocs": true
}
JSON
cat > "${passed}/preflight.json" <<JSON
{
  "matrix": ["gar"],
  "providers": [
    {
      "missing": [],
      "provider": "gar",
      "providerCLIIdentities": {
        "gcloud": {
          "authenticated": true,
          "available": true,
          "project": "project"
        }
      },
      "readyToRun": true,
      "repository": "us-docker.pkg.dev/project/repository/image"
    }
  ],
  "required": ["gar"]
}
JSON
cat > "${passed}/probe/gar.json" <<JSON
{
  "profile": "gar",
  "provider": "gar",
  "registryHost": "us-docker.pkg.dev",
  "repository": "us-docker.pkg.dev/project/repository/image",
  "result": "passed",
  "tag": "osix-probe-gar",
  "testedAt": "2026-06-22T22:50:15Z"
}
JSON
cat > "${passed}/matrix.json" <<JSON
{
  "providers": [
    {
      "logFile": "${passed}/logs/gar.log",
      "provider": "gar",
      "repository": "us-docker.pkg.dev/project/repository/image",
      "result": "passed"
    }
  ],
  "result": "passed"
}
JSON
touch "${passed}/logs/gar-probe.log" "${passed}/logs/gar.log"

"${repo_root}/scripts/summarize-hosted-registry-run.sh" "${passed}" > "${tmp}/passed.out"
grep -q "Result: passed" "${tmp}/passed.out"
grep -q "Stage: complete" "${tmp}/passed.out"
grep -q "gar:" "${tmp}/passed.out"
grep -q "probe: passed" "${tmp}/passed.out"
grep -q "matrix: passed" "${tmp}/passed.out"

gar_failed="${tmp}/gar-failed"
mkdir -p "${gar_failed}"
cp -R "${failed}/." "${gar_failed}/"
python3 - "${gar_failed}" <<'PY'
import json
import os
import sys

root = sys.argv[1]
with open(os.path.join(root, "run.json"), encoding="utf-8") as f:
    run = json.load(f)
run["providers"] = ["gar"]
run["stage"] = "probe:gar"
run["message"] = "hosted registry probe failed for gar"
run["paths"]["logs"] = os.path.join(root, "logs")
run["paths"]["matrix"] = os.path.join(root, "matrix.json")
run["paths"]["preflight"] = os.path.join(root, "preflight.json")
run["paths"]["probe"] = os.path.join(root, "probe")
with open(os.path.join(root, "run.json"), "w", encoding="utf-8") as f:
    json.dump(run, f, indent=2, sort_keys=True)
    f.write("\n")
with open(os.path.join(root, "preflight.json"), encoding="utf-8") as f:
    preflight = json.load(f)
item = preflight["providers"][0]
item["provider"] = "gar"
item["repository"] = "us-docker.pkg.dev/project/repository/image"
item["providerCLIIdentities"] = {"gcloud": {"authenticated": True, "available": True, "project": "project"}}
preflight["matrix"] = ["gar"]
preflight["required"] = ["gar"]
with open(os.path.join(root, "preflight.json"), "w", encoding="utf-8") as f:
    json.dump(preflight, f, indent=2, sort_keys=True)
    f.write("\n")
old_probe = os.path.join(root, "probe", "ecr-20260622T225015Z.json")
with open(old_probe, encoding="utf-8") as f:
    probe = json.load(f)
probe["provider"] = "gar"
probe["profile"] = "gar"
probe["repository"] = "us-docker.pkg.dev/project/repository/image"
probe["registryHost"] = "us-docker.pkg.dev"
probe["failureHint"] = "gar registry rejected blob upload with 403 Forbidden"
new_probe = os.path.join(root, "probe", "gar-20260622T225015Z.json")
with open(new_probe, "w", encoding="utf-8") as f:
    json.dump(probe, f, indent=2, sort_keys=True)
    f.write("\n")
os.remove(old_probe)
PY
"${repo_root}/scripts/summarize-hosted-registry-run.sh" "${gar_failed}" > "${tmp}/gar-failed.out"
grep -q "gcloud artifacts repositories describe repository" "${tmp}/gar-failed.out"
grep -q "gcloud artifacts repositories create repository" "${tmp}/gar-failed.out"
grep -q "gcloud auth configure-docker us-docker.pkg.dev" "${tmp}/gar-failed.out"
grep -q "OSIX_HOSTED_REGISTRY_GAR_REPO=us-docker.pkg.dev/project/repository/image ./scripts/run-hosted-registry-verification.sh --providers gar" "${tmp}/gar-failed.out"

acr_failed="${tmp}/acr-failed"
mkdir -p "${acr_failed}"
cp -R "${failed}/." "${acr_failed}/"
python3 - "${acr_failed}" <<'PY'
import json
import os
import sys

root = sys.argv[1]
with open(os.path.join(root, "run.json"), encoding="utf-8") as f:
    run = json.load(f)
run["providers"] = ["acr"]
run["stage"] = "probe:acr"
run["message"] = "hosted registry probe failed for acr"
run["paths"]["logs"] = os.path.join(root, "logs")
run["paths"]["matrix"] = os.path.join(root, "matrix.json")
run["paths"]["preflight"] = os.path.join(root, "preflight.json")
run["paths"]["probe"] = os.path.join(root, "probe")
with open(os.path.join(root, "run.json"), "w", encoding="utf-8") as f:
    json.dump(run, f, indent=2, sort_keys=True)
    f.write("\n")
with open(os.path.join(root, "preflight.json"), encoding="utf-8") as f:
    preflight = json.load(f)
item = preflight["providers"][0]
item["provider"] = "acr"
item["repository"] = "example.azurecr.io/osix-compat"
item["providerCLIIdentities"] = {"az": {"authenticated": True, "available": True, "subscriptionId": "sub"}}
preflight["matrix"] = ["acr"]
preflight["required"] = ["acr"]
with open(os.path.join(root, "preflight.json"), "w", encoding="utf-8") as f:
    json.dump(preflight, f, indent=2, sort_keys=True)
    f.write("\n")
old_probe = os.path.join(root, "probe", "ecr-20260622T225015Z.json")
with open(old_probe, encoding="utf-8") as f:
    probe = json.load(f)
probe["provider"] = "acr"
probe["profile"] = "acr"
probe["repository"] = "example.azurecr.io/osix-compat"
probe["registryHost"] = "example.azurecr.io"
probe["failureHint"] = "acr registry rejected blob upload with 403 Forbidden"
new_probe = os.path.join(root, "probe", "acr-20260622T225015Z.json")
with open(new_probe, "w", encoding="utf-8") as f:
    json.dump(probe, f, indent=2, sort_keys=True)
    f.write("\n")
os.remove(old_probe)
PY
"${repo_root}/scripts/summarize-hosted-registry-run.sh" "${acr_failed}" > "${tmp}/acr-failed.out"
grep -q "az acr login --name example" "${tmp}/acr-failed.out"
grep -q "OSIX_HOSTED_REGISTRY_ACR_REPO=example.azurecr.io/osix-compat ./scripts/run-hosted-registry-verification.sh --providers acr" "${tmp}/acr-failed.out"

echo "Hosted registry run summary smoke passed"
