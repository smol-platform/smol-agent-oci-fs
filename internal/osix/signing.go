package osix

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type signaturePayload struct {
	OSIxVersion    string    `json:"osixVersion"`
	ManifestDigest string    `json:"manifestDigest"`
	Algorithm      string    `json:"algorithm"`
	PublicKey      string    `json:"publicKey"`
	Signature      string    `json:"signature"`
	Signer         string    `json:"signer"`
	CreatedAt      time.Time `json:"createdAt"`
}

type provenancePayload struct {
	OSIxVersion    string    `json:"osixVersion"`
	ManifestDigest string    `json:"manifestDigest"`
	BaseDigest     string    `json:"baseDigest"`
	ParentDigest   string    `json:"parentDigest,omitempty"`
	SnapshotID     string    `json:"snapshotId"`
	Creator        string    `json:"creator,omitempty"`
	Tool           string    `json:"tool"`
	Attestation    string    `json:"attestation,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

type cosignSignatureMetadata struct {
	OSIxVersion   string    `json:"osixVersion"`
	PayloadDigest string    `json:"payloadDigest"`
	Algorithm     string    `json:"algorithm"`
	PublicKeyPEM  string    `json:"publicKeyPem"`
	PublicKeyID   string    `json:"publicKeyId"`
	Signature     string    `json:"signature"`
	Signer        string    `json:"signer"`
	CreatedAt     time.Time `json:"createdAt"`
}

func SignSnapshot(workspaceRoot, ref string, opts SignOptions) (VerifyResult, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return VerifyResult{}, err
	}
	digest, _, cfg, err := s.loadManifest(ref)
	if err != nil {
		return VerifyResult{}, err
	}
	if isPublicSigstoreSigner(opts.Signer) {
		return signPublicSigstoreSnapshot(s, digest, cfg, opts)
	}
	signer := opts.Signer
	attest := opts.Attest
	privateKey, publicKey, signerName, err := loadSigningKey(s, signer)
	if err != nil {
		return VerifyResult{}, err
	}
	sig := ed25519.Sign(privateKey, []byte(digest))
	now := time.Now().UTC().Truncate(time.Second)
	payload := signaturePayload{
		OSIxVersion:    Version,
		ManifestDigest: digest,
		Algorithm:      "ed25519",
		PublicKey:      base64.StdEncoding.EncodeToString(publicKey),
		Signature:      base64.StdEncoding.EncodeToString(sig),
		Signer:         signerName,
		CreatedAt:      now,
	}
	sigData, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return VerifyResult{}, err
	}
	sigDesc, err := s.writeBlob(sigData)
	if err != nil {
		return VerifyResult{}, err
	}
	sigDesc.MediaType = MediaTypeSignature
	if err := s.writeRef(signatureRefName(digest), sigDesc.Digest); err != nil {
		return VerifyResult{}, err
	}
	parent := ""
	if cfg.Parent != nil {
		parent = cfg.Parent.Digest
	}
	prov := provenancePayload{
		OSIxVersion:    Version,
		ManifestDigest: digest,
		BaseDigest:     cfg.Base.Digest,
		ParentDigest:   parent,
		SnapshotID:     cfg.Snapshot.ID,
		Creator:        cfg.Agent.CreatedBy,
		Tool:           "osix/" + Version,
		Attestation:    attest,
		CreatedAt:      now,
	}
	provData, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return VerifyResult{}, err
	}
	provDesc, err := s.writeBlob(provData)
	if err != nil {
		return VerifyResult{}, err
	}
	provDesc.MediaType = MediaTypeProvenance
	if err := s.writeRef(provenanceRefName(digest), provDesc.Digest); err != nil {
		return VerifyResult{}, err
	}
	if err := writeSigstoreCompatibilityArtifacts(s, digest, cfg, signer, signerName, attest, now); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{ManifestDigest: digest, SignatureDigest: sigDesc.Digest, ProvenanceDigest: provDesc.Digest, Signer: signerName}, nil
}

func isPublicSigstoreSigner(signer string) bool {
	switch strings.TrimSpace(signer) {
	case "sigstore-keyless", "keyless-public", "fulcio-keyless":
		return true
	default:
		return false
	}
}

func VerifySnapshot(workspaceRoot, ref string, opts VerifyOptions) (VerifyResult, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return VerifyResult{}, err
	}
	digest, _, _, err := s.loadManifest(ref)
	if err != nil {
		return VerifyResult{}, err
	}
	if opts.TrustedKey != "" && sigstorePolicyActive(opts) {
		return VerifyResult{}, fmt.Errorf("--trusted-key cannot be combined with Sigstore certificate policy flags")
	}
	if opts.TrustedKey != "" {
		trusted, err := readTrustedKey(opts.TrustedKey)
		if err != nil {
			return VerifyResult{}, err
		}
		if trusted.ecdsa != nil {
			return verifyCosignSnapshot(s, digest, trusted.ecdsa)
		}
		return verifyOSIxSnapshot(s, digest, trusted.ed25519)
	}
	if sigstorePolicyActive(opts) {
		return verifySigstoreSnapshot(s, digest, opts)
	}
	return verifyOSIxSnapshot(s, digest, nil)
}

type trustedPublicKey struct {
	ed25519 ed25519.PublicKey
	ecdsa   *ecdsa.PublicKey
}

func verifyOSIxSnapshot(s store, digest string, trusted ed25519.PublicKey) (VerifyResult, error) {
	sigDigest, err := s.resolveRef(signatureRefName(digest))
	if err != nil {
		return VerifyResult{}, fmt.Errorf("signature not found for %s: %w", digest, err)
	}
	sigData, err := s.readBlob(sigDigest)
	if err != nil {
		return VerifyResult{}, err
	}
	var payload signaturePayload
	if err := json.Unmarshal(sigData, &payload); err != nil {
		return VerifyResult{}, err
	}
	if payload.ManifestDigest != digest {
		return VerifyResult{}, fmt.Errorf("signature subject mismatch: want %s got %s", digest, payload.ManifestDigest)
	}
	publicKey, err := base64.StdEncoding.DecodeString(payload.PublicKey)
	if err != nil {
		return VerifyResult{}, err
	}
	if trusted != nil {
		if string(trusted) != string(publicKey) {
			return VerifyResult{}, fmt.Errorf("signature public key does not match trusted key")
		}
	}
	signature, err := base64.StdEncoding.DecodeString(payload.Signature)
	if err != nil {
		return VerifyResult{}, err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(digest), signature) {
		return VerifyResult{}, fmt.Errorf("signature verification failed")
	}
	provDigest, _ := s.resolveRef(provenanceRefName(digest))
	return VerifyResult{ManifestDigest: digest, SignatureDigest: sigDigest, ProvenanceDigest: provDigest, Signer: payload.Signer}, nil
}

func verifyCosignSnapshot(s store, digest string, trusted *ecdsa.PublicKey) (VerifyResult, error) {
	metaDigest, err := s.resolveRef(cosignSignatureRefName(digest))
	if err != nil {
		return VerifyResult{}, fmt.Errorf("cosign signature not found for %s: %w", digest, err)
	}
	metaData, err := s.readBlob(metaDigest)
	if err != nil {
		return VerifyResult{}, err
	}
	var meta cosignSignatureMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return VerifyResult{}, err
	}
	payloadDigest := meta.PayloadDigest
	if payloadDigest == "" {
		payloadDigest, err = s.resolveRef(cosignPayloadRefName(digest))
		if err != nil {
			return VerifyResult{}, fmt.Errorf("cosign payload not found for %s: %w", digest, err)
		}
	}
	payloadData, err := s.readBlob(payloadDigest)
	if err != nil {
		return VerifyResult{}, err
	}
	var payload struct {
		Critical struct {
			Image struct {
				Digest string `json:"Docker-manifest-digest"`
			} `json:"image"`
			Type string `json:"type"`
		} `json:"critical"`
	}
	if err := json.Unmarshal(payloadData, &payload); err != nil {
		return VerifyResult{}, fmt.Errorf("parse cosign simple-signing payload: %w", err)
	}
	if payload.Critical.Type != "cosign container image signature" {
		return VerifyResult{}, fmt.Errorf("cosign payload type mismatch: %q", payload.Critical.Type)
	}
	if payload.Critical.Image.Digest != digest {
		return VerifyResult{}, fmt.Errorf("cosign payload subject mismatch: want %s got %s", digest, payload.Critical.Image.Digest)
	}
	signature, err := base64.StdEncoding.DecodeString(meta.Signature)
	if err != nil {
		return VerifyResult{}, err
	}
	payloadHash := sha256.Sum256(payloadData)
	if !ecdsa.VerifyASN1(trusted, payloadHash[:], signature) {
		return VerifyResult{}, fmt.Errorf("cosign signature verification failed")
	}
	provDigest, _ := s.resolveRef(sigstoreAttestationBundleRefName(digest))
	if provDigest == "" {
		provDigest, _ = s.resolveRef(provenanceRefName(digest))
	}
	signer := meta.Signer
	if signer == "" {
		signer = "cosign"
	}
	return VerifyResult{ManifestDigest: digest, SignatureDigest: metaDigest, ProvenanceDigest: provDigest, Signer: signer}, nil
}

func loadSigningKey(s store, signer string) (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	if signer == "" || signer == "keyless" || signer == "keyless-local" {
		path := filepath.Join(s.root, "keys", "keyless_ed25519")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, nil, "", err
			}
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return nil, nil, "", err
			}
			if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
				return nil, nil, "", err
			}
			if err := os.WriteFile(path+".pub", []byte(base64.StdEncoding.EncodeToString(pub)), 0o644); err != nil {
				return nil, nil, "", err
			}
		}
		priv, pub, err := readPrivateKey(path)
		return priv, pub, "keyless-local", err
	}
	priv, pub, err := readPrivateKey(signer)
	return priv, pub, signer, err
}

func readPrivateKey(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("invalid ed25519 private key size")
	}
	priv := ed25519.PrivateKey(raw)
	pub := priv.Public().(ed25519.PublicKey)
	return priv, pub, nil
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid ed25519 public key size")
	}
	return ed25519.PublicKey(raw), nil
}

func readTrustedKey(path string) (trustedPublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return trustedPublicKey{}, err
	}
	block, _ := pem.Decode(data)
	if block != nil {
		pub, err := parseECDSAPublicKeyBlock(block)
		if err != nil {
			return trustedPublicKey{}, err
		}
		return trustedPublicKey{ecdsa: pub}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return trustedPublicKey{}, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return trustedPublicKey{}, fmt.Errorf("invalid trusted public key: expected ed25519 base64 or ECDSA P-256 PEM")
	}
	return trustedPublicKey{ed25519: ed25519.PublicKey(raw)}, nil
}

func parseECDSAPublicKeyBlock(block *pem.Block) (*ecdsa.PublicKey, error) {
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := pubAny.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("trusted PEM key is %T, want ECDSA public key", pubAny)
	}
	if pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("trusted ECDSA key must use P-256")
	}
	return pub, nil
}

func writeSigstoreCompatibilityArtifacts(s store, manifestDigest string, cfg AgentConfig, signer, signerName, attest string, createdAt time.Time) error {
	privateKey, publicKeyPEM, publicKeyID, err := loadCosignSigningKey(s, signer)
	if err != nil {
		return err
	}
	payloadData, err := json.Marshal(cosignSimpleSigningPayload(manifestDigest, signerName, createdAt))
	if err != nil {
		return err
	}
	payloadHash := sha256.Sum256(payloadData)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, payloadHash[:])
	if err != nil {
		return err
	}
	payloadDesc, err := s.writeBlob(payloadData)
	if err != nil {
		return err
	}
	if err := s.writeRef(cosignPayloadRefName(manifestDigest), payloadDesc.Digest); err != nil {
		return err
	}
	meta := cosignSignatureMetadata{
		OSIxVersion:   Version,
		PayloadDigest: payloadDesc.Digest,
		Algorithm:     "ecdsa-p256-sha256",
		PublicKeyPEM:  string(publicKeyPEM),
		PublicKeyID:   publicKeyID,
		Signature:     base64.StdEncoding.EncodeToString(signature),
		Signer:        signerName,
		CreatedAt:     createdAt,
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	metaDesc, err := s.writeBlob(metaData)
	if err != nil {
		return err
	}
	if err := s.writeRef(cosignSignatureRefName(manifestDigest), metaDesc.Digest); err != nil {
		return err
	}
	signatureBundle, err := sigstoreMessageSignatureBundle(manifestDigest, signature, publicKeyID)
	if err != nil {
		return err
	}
	signatureBundleDesc, err := s.writeBlob(signatureBundle)
	if err != nil {
		return err
	}
	if err := s.writeRef(sigstoreSignatureBundleRefName(manifestDigest), signatureBundleDesc.Digest); err != nil {
		return err
	}
	attestationBundle, err := sigstoreDSSEProvenanceBundle(manifestDigest, cfg, privateKey, publicKeyID, attest, createdAt)
	if err != nil {
		return err
	}
	attestationBundleDesc, err := s.writeBlob(attestationBundle)
	if err != nil {
		return err
	}
	return s.writeRef(sigstoreAttestationBundleRefName(manifestDigest), attestationBundleDesc.Digest)
}

func cosignSimpleSigningPayload(manifestDigest, signerName string, createdAt time.Time) map[string]any {
	return map[string]any{
		"critical": map[string]any{
			"identity": map[string]string{
				"docker-reference": "",
			},
			"image": map[string]string{
				"Docker-manifest-digest": manifestDigest,
			},
			"type": "cosign container image signature",
		},
		"optional": map[string]any{
			"creator":     "osix/" + Version,
			"timestamp":   createdAt.Unix(),
			"osixVersion": Version,
			"signer":      signerName,
		},
	}
}

func loadCosignSigningKey(s store, signer string) (*ecdsa.PrivateKey, []byte, string, error) {
	path := strings.TrimSpace(signer)
	if path == "" || path == "keyless" {
		path = filepath.Join(s.root, "keys", "cosign_ecdsa_p256.pem")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, nil, "", err
		}
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, "", err
		}
		privDER, err := x509.MarshalECPrivateKey(priv)
		if err != nil {
			return nil, nil, "", err
		}
		privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
		if err := os.WriteFile(path, privPEM, 0o600); err != nil {
			return nil, nil, "", err
		}
		pubPEM, _, err := ecdsaPublicKeyPEM(&priv.PublicKey)
		if err != nil {
			return nil, nil, "", err
		}
		if err := os.WriteFile(path+".pub", pubPEM, 0o644); err != nil {
			return nil, nil, "", err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, nil, "", fmt.Errorf("cosign-compatible signing requires an ECDSA P-256 PEM private key: %s", path)
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return nil, nil, "", err
		}
		var ok bool
		priv, ok = parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, nil, "", fmt.Errorf("cosign-compatible signing key %s is not ECDSA", path)
		}
	}
	if priv.Curve != elliptic.P256() {
		return nil, nil, "", fmt.Errorf("cosign-compatible signing key %s must use P-256", path)
	}
	pubPEM, keyID, err := ecdsaPublicKeyPEM(&priv.PublicKey)
	return priv, pubPEM, keyID, err
}

func ecdsaPublicKeyPEM(pub *ecdsa.PublicKey) ([]byte, string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(der)
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sigstoreMessageSignatureBundle(manifestDigest string, signature []byte, publicKeyID string) ([]byte, error) {
	digestRaw, err := digestRawBytes(manifestDigest)
	if err != nil {
		return nil, err
	}
	bundle := map[string]any{
		"mediaType": MediaTypeSigstoreBundle,
		"verificationMaterial": map[string]any{
			"publicKey": map[string]string{
				"hint": publicKeyID,
			},
		},
		"messageSignature": map[string]any{
			"messageDigest": map[string]string{
				"algorithm": "SHA2_256",
				"digest":    base64.StdEncoding.EncodeToString(digestRaw),
			},
			"signature": base64.StdEncoding.EncodeToString(signature),
		},
	}
	return json.MarshalIndent(bundle, "", "  ")
}

func sigstoreDSSEProvenanceBundle(manifestDigest string, cfg AgentConfig, privateKey *ecdsa.PrivateKey, publicKeyID, attest string, createdAt time.Time) ([]byte, error) {
	subjectHex := strings.TrimPrefix(manifestDigest, "sha256:")
	parent := ""
	if cfg.Parent != nil {
		parent = cfg.Parent.Digest
	}
	statement := map[string]any{
		"_type": "https://in-toto.io/Statement/v1",
		"subject": []map[string]any{{
			"name": "osix snapshot",
			"digest": map[string]string{
				"sha256": subjectHex,
			},
		}},
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"buildType": "https://github.com/smol-platform/smol-agent-oci-fs/osix-snapshot/v0.1",
				"externalParameters": map[string]any{
					"baseDigest":   cfg.Base.Digest,
					"parentDigest": parent,
					"snapshotId":   cfg.Snapshot.ID,
					"attestation":  attest,
				},
			},
			"runDetails": map[string]any{
				"builder": map[string]string{
					"id": "osix/" + Version,
				},
				"metadata": map[string]string{
					"invocationId": cfg.Snapshot.ID,
					"startedOn":    createdAt.Format(time.RFC3339),
					"finishedOn":   createdAt.Format(time.RFC3339),
				},
			},
		},
	}
	payload, err := json.Marshal(statement)
	if err != nil {
		return nil, err
	}
	pae := dssePAE("application/vnd.in-toto+json", payload)
	hash := sha256.Sum256(pae)
	sig, err := ecdsa.SignASN1(rand.Reader, privateKey, hash[:])
	if err != nil {
		return nil, err
	}
	bundle := map[string]any{
		"mediaType": MediaTypeSigstoreBundle,
		"verificationMaterial": map[string]any{
			"publicKey": map[string]string{
				"hint": publicKeyID,
			},
		},
		"dsseEnvelope": map[string]any{
			"payload":     base64.StdEncoding.EncodeToString(payload),
			"payloadType": "application/vnd.in-toto+json",
			"signatures": []map[string]string{{
				"keyid": publicKeyID,
				"sig":   base64.StdEncoding.EncodeToString(sig),
			}},
		},
	}
	return json.MarshalIndent(bundle, "", "  ")
}

func dssePAE(payloadType string, payload []byte) []byte {
	return []byte(fmt.Sprintf("DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload))
}

func digestRawBytes(digest string) ([]byte, error) {
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	raw, err := hex.DecodeString(hexDigest)
	if err != nil {
		return nil, fmt.Errorf("decode digest %s: %w", digest, err)
	}
	if len(raw) != sha256.Size {
		return nil, fmt.Errorf("digest %s is %d bytes, want %d", digest, len(raw), sha256.Size)
	}
	return raw, nil
}

func signatureRefName(digest string) string {
	return "signature-" + strings.TrimPrefix(digest, "sha256:")
}

func provenanceRefName(digest string) string {
	return "provenance-" + strings.TrimPrefix(digest, "sha256:")
}

func cosignPayloadRefName(digest string) string {
	return "cosign-payload-" + strings.TrimPrefix(digest, "sha256:")
}

func cosignSignatureRefName(digest string) string {
	return "cosign-signature-" + strings.TrimPrefix(digest, "sha256:")
}

func sigstoreSignatureBundleRefName(digest string) string {
	return "sigstore-signature-bundle-" + strings.TrimPrefix(digest, "sha256:")
}

func sigstoreAttestationBundleRefName(digest string) string {
	return "sigstore-attestation-bundle-" + strings.TrimPrefix(digest, "sha256:")
}

func cosignSignatureTag(digest string) string {
	return strings.ReplaceAll(digest, ":", "-") + ".sig"
}

func sigstoreReferrersTag(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}
