package k8soperator

import "strings"

func RenderInstallManifests() string {
	parts := []string{
		crdAgentOCIFileSystem,
		crdAgentOCISnapshotPolicy,
		crdAgentOCISnapshot,
		crdAgentOCIRuntimeClass,
		rbacManifest,
		operatorDeployment,
		csiNodeDaemonSet,
		storageClassManifest,
	}
	return strings.Join(parts, "\n---\n") + "\n"
}

const crdAgentOCIFileSystem = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: agentocifilesystems.agent.smol.ai
spec:
  group: agent.smol.ai
  scope: Namespaced
  names:
    plural: agentocifilesystems
    singular: agentocifilesystem
    kind: AgentOCIFileSystem
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        required: [spec]
        properties:
          spec:
            type: object
            required: [baseImage, stateRef]
            properties:
              baseImage: {type: string}
              stateRef: {type: string}
              branch: {type: string}
              sourceRef: {type: string}
              mountMode:
                type: string
                enum: [auto, overlay, fuse, materialized]
              registrySecretRef:
                type: object
                properties:
                  name: {type: string}
              encryption:
                type: object
                properties:
                  recipients: {type: string}
                  secretRef:
                    type: object
                    properties:
                      name: {type: string}
                      key: {type: string}
              signing:
                type: object
                properties:
                  signer: {type: string}
                  attestation: {type: string}
                  trustedKeySecretRef:
                    type: object
                    properties:
                      name: {type: string}
                      key: {type: string}
                  identityTokenSecretRef:
                    type: object
                    properties:
                      name: {type: string}
                      key: {type: string}
              snapshotPolicyRef:
                type: object
                properties:
                  name: {type: string}
              runtimeClassRef:
                type: object
                properties:
                  name: {type: string}
          status:
            type: object
            x-kubernetes-preserve-unknown-fields: true
    subresources:
      status: {}`

const crdAgentOCISnapshotPolicy = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: agentocisnapshotpolicies.agent.smol.ai
spec:
  group: agent.smol.ai
  scope: Namespaced
  names:
    plural: agentocisnapshotpolicies
    singular: agentocisnapshotpolicy
    kind: AgentOCISnapshotPolicy
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              every: {type: string}
              maxDirtyBytes: {type: string}
              onTurnBoundary: {type: boolean}
              push: {type: boolean}
              compactEvery: {type: integer}
              squashEvery: {type: integer}
              checkpointTagPrefix: {type: string}
              keepSnapshots:
                type: array
                items: {type: string}
              preserveSigned: {type: boolean}
              pruneLocal: {type: boolean}
              pruneRemote: {type: boolean}
          status:
            type: object
            x-kubernetes-preserve-unknown-fields: true
    subresources:
      status: {}`

const crdAgentOCISnapshot = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: agentocisnapshots.agent.smol.ai
spec:
  group: agent.smol.ai
  scope: Namespaced
  names:
    plural: agentocisnapshots
    singular: agentocisnapshot
    kind: AgentOCISnapshot
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              fileSystemName: {type: string}
              fileSystemUID: {type: string}
              snapshotDigest: {type: string}
              parentDigest: {type: string}
              branch: {type: string}
              checkpointDigest: {type: string}
          status:
            type: object
            x-kubernetes-preserve-unknown-fields: true
    subresources:
      status: {}`

const crdAgentOCIRuntimeClass = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: agentociruntimeclasses.agent.smol.ai
spec:
  group: agent.smol.ai
  scope: Cluster
  names:
    plural: agentociruntimeclasses
    singular: agentociruntimeclass
    kind: AgentOCIRuntimeClass
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              mountMode: {type: string}
              cacheRoot: {type: string}
              runtimeImage: {type: string}
              privilegedOverlay: {type: boolean}
              fuse: {type: boolean}
              lazyFuse: {type: boolean}
              nodeSelector:
                type: object
                additionalProperties: {type: string}
          status:
            type: object
            x-kubernetes-preserve-unknown-fields: true
    subresources:
      status: {}`

const rbacManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: osix-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: osix-operator
  namespace: osix-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: osix-operator
rules:
- apiGroups: ["agent.smol.ai"]
  resources: ["agentocifilesystems", "agentocisnapshotpolicies", "agentocisnapshots", "agentociruntimeclasses"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
- apiGroups: ["agent.smol.ai"]
  resources: ["agentocifilesystems/status", "agentocisnapshotpolicies/status", "agentocisnapshots/status", "agentociruntimeclasses/status"]
  verbs: ["get", "update", "patch"]
- apiGroups: [""]
  resources: ["events", "secrets", "persistentvolumes", "persistentvolumeclaims", "pods", "nodes"]
  verbs: ["get", "list", "watch", "create", "patch"]
- apiGroups: ["storage.k8s.io"]
  resources: ["storageclasses", "csinodes"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: osix-operator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: osix-operator
subjects:
- kind: ServiceAccount
  name: osix-operator
  namespace: osix-system`

const operatorDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: osix-operator
  namespace: osix-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: osix-operator
  template:
    metadata:
      labels:
        app: osix-operator
    spec:
      serviceAccountName: osix-operator
      containers:
      - name: operator
        image: ghcr.io/smol-platform/smol-agent-oci-fs-operator:latest
        args: ["serve"]
        ports:
        - containerPort: 8080
          name: health
        readinessProbe:
          httpGet:
            path: /readyz
            port: health
        livenessProbe:
          httpGet:
            path: /healthz
            port: health`

const csiNodeDaemonSet = `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: osix-csi-node
  namespace: osix-system
spec:
  selector:
    matchLabels:
      app: osix-csi-node
  template:
    metadata:
      labels:
        app: osix-csi-node
    spec:
      serviceAccountName: osix-operator
      containers:
      - name: node
        image: ghcr.io/smol-platform/smol-agent-oci-fs-csi:latest
        args: ["serve", "--addr", ":8081"]
        ports:
        - containerPort: 8081
          name: health
        readinessProbe:
          httpGet:
            path: /readyz
            port: health
        livenessProbe:
          httpGet:
            path: /healthz
            port: health
        securityContext:
          privileged: true
        volumeMounts:
        - name: kubelet
          mountPath: /var/lib/kubelet
          mountPropagation: Bidirectional
        - name: osix-workspaces
          mountPath: /var/lib/osix
        - name: dev-fuse
          mountPath: /dev/fuse
      volumes:
      - name: kubelet
        hostPath:
          path: /var/lib/kubelet
      - name: osix-workspaces
        hostPath:
          path: /var/lib/osix
          type: DirectoryOrCreate
      - name: dev-fuse
        hostPath:
          path: /dev/fuse
          type: CharDevice`

const storageClassManifest = `apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: osix-agent-state
provisioner: agent.smol.ai/osix
volumeBindingMode: WaitForFirstConsumer
parameters:
  mountMode: auto`
