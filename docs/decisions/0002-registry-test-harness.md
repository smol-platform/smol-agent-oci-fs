# Decision 0002: Registry Test Harness For M2

## Status

Accepted.

## Context

M2 needs integration tests that prove OSIx blobs, configs, manifests, and tags can move through a real OCI registry. The local `.osix` content store is enough for M1, but it is not a registry compatibility test.

## Decision

M2 will use the upstream Docker Distribution registry as the first local fixture registry.

The test harness should run `registry:2` when Docker or another OCI-compatible container runtime is available. Tests should push and pull OSIx image manifests in `mode=image` first, because that is the required compatibility fallback. Referrer and hybrid-mode tests can be added after the basic push/pull path is stable.

## Consequences

This gives M2 a concrete, widely used local target without coupling the prototype to a hosted registry.

The current CLI does not implement M2 yet. The selected harness is documented so registry work can start from a known target.

