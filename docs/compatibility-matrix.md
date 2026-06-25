# Registry Compatibility Matrix

| Target | Mode | Status | Evidence |
| --- | --- | --- | --- |
| In-process OCI Distribution fixture | `image` | Supported | `TestPushPullSnapshotThroughOCIRegistry` pushes blobs/config/manifests, resolves tags, pulls parent chains, and restores. |
| Docker Distribution `registry:2` | `image` + fallback artifact tags | Supported | `scripts/test-registry-docker.sh` starts `registry:2`, signs a snapshot, pushes it with `osix push`, pulls it with `osix pull`, verifies signature/provenance after pull, and restores the pulled ref. |
| GitHub Container Registry | `image` + fallback artifact tags + auth | Supported | `OSIX_HOSTED_REGISTRY_REPO=ghcr.io/smol-platform/smol-agent-oci-fs-compat scripts/test-registry-hosted.sh` pushed a signed snapshot with a unique tag, pulled it, verified signature/provenance, and restored content using existing GHCR credentials. |
| AWS ECR | `image` + fallback artifact tags + auth | Supported | `scripts/test-registry-hosted-matrix.sh` passed for `022499027873.dkr.ecr.us-east-2.amazonaws.com/osix-compat` on 2026-06-23T15:17:33Z using Docker config; remote tag `compat-20260623111724-50217` covered init, snapshot, keyless-sign, verify-local, push, pull, verify-pulled, restore, content-check. Evidence: `../hosted-registry-run-ecr-sandbox/evidence/ecr-20260623T151733Z.json`; aggregate report: `../hosted-registry-run-ecr-sandbox/matrix.json`. |
| Google Artifact Registry | `image` + fallback artifact tags + auth | Harnessed, pending live evidence | `scripts/test-registry-hosted.sh` has a `gar` profile for `REGION-docker.pkg.dev/PROJECT/REPOSITORY/IMAGE`; `scripts/test-registry-hosted-matrix.sh` can run it through `OSIX_HOSTED_REGISTRY_GAR_REPO`; `OSIX_HOSTED_REGISTRY_EVIDENCE_DIR` writes JSON evidence for successful live runs; `TestLoadDockerCredentialHelperCredentials` verifies GAR-style Docker helper bearer tokens. A live push/pull run still requires a provisioned GAR image path and credentials. |
| Azure Container Registry | `image` + fallback artifact tags + auth | Harnessed, pending live evidence | `scripts/test-registry-hosted.sh` has an `acr` profile for `REGISTRY.azurecr.io/REPO`; `scripts/test-registry-hosted-matrix.sh` can run it through `OSIX_HOSTED_REGISTRY_ACR_REPO`; `OSIX_HOSTED_REGISTRY_EVIDENCE_DIR` writes JSON evidence for successful live runs; `TestLoadDockerCredentialHelperCredentials` and `TestLoadDockerCredentialStoreCredentials` verify ACR-style Docker helper and global `credsStore` credentials. A live push/pull run still requires a provisioned ACR repo and credentials. |
| Generic OCI registry with custom media types | `image` | Expected compatible | OSIx publishes normal OCI image manifests with custom config/layer media types. |
| Referrer/hybrid registry mode | `hybrid` | Supported for OSIx and Sigstore artifact discovery | `TestPushPullSignedSnapshotReferrersThroughOCIRegistry` publishes OSIx signature/provenance referrers plus Sigstore bundle referrers, writes the Sigstore `sha256-<digest>` referrers-tag index, publishes the cosign `sha256-<digest>.sig` simple-signing image, pulls the artifacts, verifies the OSIx signature after pull, and verifies local cosign signatures with a trusted ECDSA P-256 public key. |
| Public Sigstore bundle verification | bundle policy | Supported for verification | `TestVerifySigstorePublicGoodBundleWithCertificatePolicy` verifies a public-good Sigstore bundle against artifact digest, Fulcio certificate identity, OIDC issuer, Rekor transparency-log material, observer timestamp, and certificate SCT policy. |
| Public Sigstore keyless signing | signing policy | Supported | `TestSnapshotSigstoreKeylessEmitsCertificateBundle` signs a snapshot through the `sigstore-keyless` path with Fulcio-style certificate material, emits cosign/Sigstore artifacts, and verifies the certificate-backed bundle through `VerifySnapshot` policy. |

## Fallback Behavior

The v0 CLI defaults to image-manifest compatibility. This means a snapshot is
restorable from the manifest, config blob, and layer blobs without requiring OCI
1.1 Referrers API support. Signature and provenance artifacts are also published
under deterministic fallback tags so verification can work on registries that
store subject-bearing manifests but do not list referrers. For Sigstore/cosign
interoperability, OSIx additionally writes:

- cosign simple-signing image tag: `sha256-<subject-hex>.sig`
- Sigstore bundle referrers-index tag: `sha256-<subject-hex>`
- bundle manifests with artifact type `application/vnd.dev.sigstore.bundle.v0.3+json`

Before live ECR/GAR/ACR runs, use `OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT=path
OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY=1 scripts/test-registry-hosted-matrix.sh`
to write a machine-readable readiness report. The report records missing
repository variables, provider-specific repository shape validation, provider
CLI availability, OSIx env credentials, Docker credential hosts, Docker
credential-helper executable availability, provider cloud CLI identity status,
and whether Docker credentials match the configured registry host. In preflight mode,
`OSIX_HOSTED_REGISTRY_REQUIRED=ecr,gar,acr` fails when any required provider is
missing its repository, has a malformed repository, lacks its provider CLI, or
lacks usable credentials. `scripts/test-registry-hosted-preflight.sh`
regression-tests this readiness contract without live registry credentials.
`scripts/doctor-hosted-registry.sh` renders the same preflight data as a
human-readable readiness summary; `scripts/test-registry-hosted-doctor.sh`
regression-tests that output without live registry credentials.
`scripts/test-registry-hosted-local.sh` runs the full hosted local smoke suite
before attempting live ECR/GAR/ACR verification.
When Docker credentials exist for known hosted registry hosts but repo variables
are unset, the preflight report includes `credentialedRegistryHosts` and
`suggestedRepositoryExports` to help construct the missing
`OSIX_HOSTED_REGISTRY_*_REPO` values.
Use `scripts/setup-hosted-registry-env.sh` to generate the provider-specific
repository exports and review or run supported cloud create/login commands;
`scripts/test-registry-hosted-setup.sh` regression-tests that setup helper
without cloud credentials. Pass `--env-file hosted-registry.env` to write the
exports for the verification runner. When `--create` is used for ACR, pass
`--acr-resource-group`; `--acr-location` and `--acr-sku` default to `eastus` and
`Basic`.
After the repository variables and credentials are configured,
`scripts/run-hosted-registry-verification.sh --providers ecr,gar,acr --env-file
hosted-registry.env` runs the
preflight, lightweight `osix registry probe` write/read check, live matrix,
evidence verifier, and compatibility-matrix update in one command;
`scripts/test-registry-hosted-runner.sh` regression-tests that driver without
live registry credentials. Probe evidence is written for passed and failed runs;
recognized failed probes include `failureClass`, `failureHint`, `exitCode`, and
a durable `logFile` beside the probe JSON.
`scripts/verify-hosted-registry-probes.sh` validates that required providers
have passed probe evidence with config/blob/manifest write and read operations
before the full signed snapshot matrix is treated as ready to run.
`scripts/verify-hosted-registry-run.sh` verifies a completed output directory
as one support gate: required-provider preflight readiness, probe evidence,
full matrix evidence, provider logs, linked matrix logs, and the top-level
`run.json` result summary.
`scripts/summarize-hosted-registry-run.sh` renders the same output directory as
a human-readable triage report for handoff when a provider still fails,
including provider-specific create/login/rerun commands for recognized
ECR/GAR/ACR registry write/auth failures.
During the live run, set `OSIX_HOSTED_REGISTRY_MATRIX_REPORT=path` to emit an
aggregate JSON result with passed/skipped/failed provider counts and
per-provider outcomes. Recognized failures include `failureClass` and
`failureHint`. When `OSIX_HOSTED_REGISTRY_LOG_DIR` is set, provider events
include `logFile`; when `OSIX_HOSTED_REGISTRY_EVIDENCE_DIR` is also set, passed
provider events include the newly written evidence file paths.
`scripts/test-registry-hosted-matrix-report.sh` regression-tests that report path
without live registry credentials. Before changing ECR, GAR, or ACR from
pending to supported, run `scripts/verify-hosted-registry-evidence.sh
MATRIX_REPORT ecr,gar,acr`; `scripts/test-registry-hosted-evidence.sh`
regression-tests that verifier without live registry credentials. After the
verifier passes, run `scripts/update-hosted-registry-compatibility.sh
MATRIX_REPORT docs/compatibility-matrix.md` to update the hosted rows from the
verified evidence; `scripts/test-registry-hosted-compatibility-update.sh`
regression-tests that updater without live registry credentials.

## Registry Tag Conflict Checks

Mutable tag movement remains optimistic because OCI Distribution does not define
a portable compare-and-swap tag update. For branch-like tags, use
`--expected-parent DIGEST` on `osix snapshot --push` or `osix push`; OSIx checks
the current remote tag digest before uploading and fails with a remote branch
conflict if the tag no longer points at the expected parent. This narrows the
race window and gives portable conflict detection. The CLI returns exit code `3`
for this typed conflict so automation can distinguish stale branch heads from
generic push failures. It is still not an atomic registry-side CAS.

Provider-specific controls reviewed:

| Provider | Control | CAS assessment |
| --- | --- | --- |
| AWS ECR | `image` + fallback artifact tags + auth | Supported | `scripts/test-registry-hosted-matrix.sh` passed for `022499027873.dkr.ecr.us-east-2.amazonaws.com/osix-compat` on 2026-06-23T15:17:33Z using Docker config; remote tag `compat-20260623111724-50217` covered init, snapshot, keyless-sign, verify-local, push, pull, verify-pulled, restore, content-check. Evidence: `../hosted-registry-run-ecr-sandbox/evidence/ecr-20260623T151733Z.json`; aggregate report: `../hosted-registry-run-ecr-sandbox/matrix.json`. |
| Google Artifact Registry | Docker repository tag immutability. See [Artifact Registry pushing and pulling](https://docs.cloud.google.com/artifact-registry/docs/docker/pushing-and-pulling). | Prevents reusing a tag for a different digest when enabled, but does not provide branch-style compare-and-swap updates. |
| Azure Container Registry | Per-tag or repository `write-enabled` locks. See [ACR image locking](https://learn.microsoft.com/en-us/azure/container-registry/container-registry-image-lock). | Can lock a tag or manifest against updates, but branch movement still needs an external coordination policy. |

## Known Registry Limits

- Registry-side compare-and-swap is not portable across all registries.
- Hosted registry verification currently has live evidence for GHCR. ECR, GAR, and ACR now have provider-specific harness profiles and credential-helper coverage, but still need live push/pull runs against provisioned repositories before they can be marked supported.
- Public Sigstore signing requires an OIDC identity token from `--sigstore-identity-token`, `--sigstore-identity-token-file`, or `SIGSTORE_ID_TOKEN`; the CLI does not run an interactive browser OIDC flow.
- Lazy single-file reads are implemented for unencrypted remote layers through `pull --lazy` and `read`. Lazy-pulled unencrypted refs can be restored by fetching and caching missing whole layers during materialization. Darwin FSKit and Linux native lazy FUSE can service `--lazy` lower reads through snapshot metadata plus lazy read paths without restoring the lowerdir first; Linux native lazy FUSE also supports writable copy-up and whiteouts. Age-only, legacy KMS, and OSIx-envelope encrypted snapshots emit encrypted per-file and chunked lazy blobs for `read --decrypt`; `read --offset N --length N --decrypt ...` can fetch only needed encrypted lazy chunks, including through Darwin FSKit and Linux lazy FUSE when decrypt material is supplied. When encrypted lazy indexes are present, `restore --decrypt` can restore from encrypted lazy blobs without downloading/decrypting the whole encrypted layer. Linux kernel overlay runtime preparation still materializes whole lower layers.
