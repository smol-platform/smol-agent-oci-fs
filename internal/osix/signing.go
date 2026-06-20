package osix

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

func SignSnapshot(workspaceRoot, ref, signer, attest string) (VerifyResult, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return VerifyResult{}, err
	}
	digest, _, cfg, err := s.loadManifest(ref)
	if err != nil {
		return VerifyResult{}, err
	}
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
	return VerifyResult{ManifestDigest: digest, SignatureDigest: sigDesc.Digest, ProvenanceDigest: provDesc.Digest, Signer: signerName}, nil
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
	if opts.TrustedKey != "" {
		trusted, err := readPublicKey(opts.TrustedKey)
		if err != nil {
			return VerifyResult{}, err
		}
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

func loadSigningKey(s store, signer string) (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	if signer == "" || signer == "keyless" {
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

func signatureRefName(digest string) string {
	return "signature-" + strings.TrimPrefix(digest, "sha256:")
}

func provenanceRefName(digest string) string {
	return "provenance-" + strings.TrimPrefix(digest, "sha256:")
}
