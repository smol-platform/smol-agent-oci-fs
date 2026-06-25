package osix

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	sigroot "github.com/sigstore/sigstore-go/pkg/root"
	sigsign "github.com/sigstore/sigstore-go/pkg/sign"
	sigtuf "github.com/sigstore/sigstore-go/pkg/tuf"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	newSigstoreEphemeralKeypair = sigsign.NewEphemeralKeypair
	signSigstoreBundle          = sigsign.Bundle
	newSigstoreFulcio           = func(opts *sigsign.FulcioOptions) sigsign.CertificateProvider {
		return sigsign.NewFulcio(opts)
	}
	newSigstoreRekor = func(opts *sigsign.RekorOptions) sigsign.Transparency {
		return sigsign.NewRekor(opts)
	}
	newSigstoreTimestampAuthority = sigsign.NewTimestampAuthority
)

func signPublicSigstoreSnapshot(s store, manifestDigest string, cfg AgentConfig, opts SignOptions) (VerifyResult, error) {
	signOpts := opts.Sigstore
	token, err := sigstoreIdentityToken(signOpts)
	if err != nil {
		return VerifyResult{}, err
	}
	manifestData, err := s.readBlob(manifestDigest)
	if err != nil {
		return VerifyResult{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	keypair, err := newSigstoreEphemeralKeypair(nil)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("generate Sigstore ephemeral keypair: %w", err)
	}
	bundleOptions, err := sigstoreSigningBundleOptions(signOpts, token)
	if err != nil {
		return VerifyResult{}, err
	}
	payloadData, err := json.Marshal(cosignSimpleSigningPayload(manifestDigest, "sigstore-keyless", now))
	if err != nil {
		return VerifyResult{}, err
	}
	payloadSignature, _, err := keypair.SignData(context.Background(), payloadData)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("sign cosign simple-signing payload: %w", err)
	}
	payloadDesc, err := s.writeBlob(payloadData)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := s.writeRef(cosignPayloadRefName(manifestDigest), payloadDesc.Digest); err != nil {
		return VerifyResult{}, err
	}
	publicKeyPEM, err := keypair.GetPublicKeyPem()
	if err != nil {
		return VerifyResult{}, err
	}
	meta := cosignSignatureMetadata{
		OSIxVersion:   Version,
		PayloadDigest: payloadDesc.Digest,
		Algorithm:     "sigstore-keyless-ecdsa-p256-sha256",
		PublicKeyPEM:  publicKeyPEM,
		PublicKeyID:   string(keypair.GetHint()),
		Signature:     base64.StdEncoding.EncodeToString(payloadSignature),
		Signer:        "sigstore-keyless",
		CreatedAt:     now,
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return VerifyResult{}, err
	}
	metaDesc, err := s.writeBlob(metaData)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := s.writeRef(cosignSignatureRefName(manifestDigest), metaDesc.Digest); err != nil {
		return VerifyResult{}, err
	}

	signatureBundle, err := signSigstoreBundle(&sigsign.PlainData{Data: manifestData}, keypair, bundleOptions)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("create Sigstore signature bundle: %w", err)
	}
	signatureBundleData, err := protojson.Marshal(signatureBundle)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("marshal Sigstore signature bundle: %w", err)
	}
	signatureBundleDesc, err := s.writeBlob(signatureBundleData)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := s.writeRef(sigstoreSignatureBundleRefName(manifestDigest), signatureBundleDesc.Digest); err != nil {
		return VerifyResult{}, err
	}

	statementData, err := sigstoreProvenanceStatement(manifestDigest, cfg, opts.Attest, now)
	if err != nil {
		return VerifyResult{}, err
	}
	attestationBundle, err := signSigstoreBundle(&sigsign.DSSEData{Data: statementData, PayloadType: "application/vnd.in-toto+json"}, keypair, bundleOptions)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("create Sigstore attestation bundle: %w", err)
	}
	attestationBundleData, err := protojson.Marshal(attestationBundle)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("marshal Sigstore attestation bundle: %w", err)
	}
	attestationBundleDesc, err := s.writeBlob(attestationBundleData)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := s.writeRef(sigstoreAttestationBundleRefName(manifestDigest), attestationBundleDesc.Digest); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{
		ManifestDigest:   manifestDigest,
		SignatureDigest:  signatureBundleDesc.Digest,
		ProvenanceDigest: attestationBundleDesc.Digest,
		Signer:           "sigstore-keyless",
	}, nil
}

func sigstoreIdentityToken(opts SigstoreSignOptions) (string, error) {
	if opts.IdentityToken != "" && opts.IdentityTokenFile != "" {
		return "", fmt.Errorf("--sigstore-identity-token and --sigstore-identity-token-file cannot both be set")
	}
	if opts.IdentityToken != "" {
		return opts.IdentityToken, nil
	}
	if opts.IdentityTokenFile != "" {
		data, err := os.ReadFile(opts.IdentityTokenFile)
		if err != nil {
			return "", fmt.Errorf("read Sigstore identity token file: %w", err)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", fmt.Errorf("Sigstore identity token file is empty")
		}
		return token, nil
	}
	if token := strings.TrimSpace(os.Getenv("SIGSTORE_ID_TOKEN")); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("sigstore-keyless signing requires --sigstore-identity-token, --sigstore-identity-token-file, or SIGSTORE_ID_TOKEN")
}

func sigstoreSigningBundleOptions(opts SigstoreSignOptions, identityToken string) (sigsign.BundleOptions, error) {
	bundleOptions := sigsign.BundleOptions{
		CertificateProviderOptions: &sigsign.CertificateProviderOptions{IDToken: identityToken},
		Context:                    context.Background(),
	}
	signingConfig, err := sigstoreSigningConfig(opts)
	if err != nil {
		return sigsign.BundleOptions{}, err
	}
	fulcioURL := opts.FulcioURL
	if fulcioURL == "" {
		if signingConfig == nil {
			return sigsign.BundleOptions{}, fmt.Errorf("sigstore-keyless signing requires --sigstore-fulcio-url or signing configuration from --sigstore-signing-config/TUF")
		}
		fulcioURL, err = sigroot.SelectService(signingConfig.FulcioCertificateAuthorityURLs(), []uint32{1}, time.Now())
		if err != nil {
			return sigsign.BundleOptions{}, fmt.Errorf("select Fulcio service: %w", err)
		}
	}
	bundleOptions.CertificateProvider = newSigstoreFulcio(&sigsign.FulcioOptions{
		BaseURL: fulcioURL,
		Timeout: 30 * time.Second,
		Retries: 1,
	})

	needsTrustedRoot := !opts.NoRekor || opts.TimestampURL != "" || (!opts.NoTimestamp && signingConfig != nil && len(signingConfig.TimestampAuthorityURLs()) > 0)
	if needsTrustedRoot {
		trustedRoot, err := sigstoreSigningTrustedRoot(opts)
		if err != nil {
			return sigsign.BundleOptions{}, err
		}
		bundleOptions.TrustedRoot = trustedRoot
	}
	if !opts.NoRekor {
		rekorURL := opts.RekorURL
		if rekorURL == "" {
			if signingConfig == nil {
				return sigsign.BundleOptions{}, fmt.Errorf("sigstore-keyless signing requires --sigstore-rekor-url or signing configuration from --sigstore-signing-config/TUF")
			}
			rekorURLs, err := sigroot.SelectServices(signingConfig.RekorLogURLs(), signingConfig.RekorLogURLsConfig(), []uint32{1}, time.Now())
			if err != nil {
				return sigsign.BundleOptions{}, fmt.Errorf("select Rekor service: %w", err)
			}
			if len(rekorURLs) == 0 {
				return sigsign.BundleOptions{}, fmt.Errorf("signing configuration has no Rekor service")
			}
			rekorURL = rekorURLs[0]
		}
		bundleOptions.TransparencyLogs = append(bundleOptions.TransparencyLogs, newSigstoreRekor(&sigsign.RekorOptions{
			BaseURL: rekorURL,
			Timeout: 90 * time.Second,
			Retries: 1,
		}))
	}
	if !opts.NoTimestamp {
		timestampURL := opts.TimestampURL
		if timestampURL == "" && signingConfig != nil && len(signingConfig.TimestampAuthorityURLs()) > 0 {
			timestampURLs, err := sigroot.SelectServices(signingConfig.TimestampAuthorityURLs(), signingConfig.TimestampAuthorityURLsConfig(), []uint32{1}, time.Now())
			if err != nil {
				return sigsign.BundleOptions{}, fmt.Errorf("select timestamp authority service: %w", err)
			}
			if len(timestampURLs) > 0 {
				timestampURL = timestampURLs[0]
			}
		}
		if timestampURL != "" {
			bundleOptions.TimestampAuthorities = append(bundleOptions.TimestampAuthorities, newSigstoreTimestampAuthority(&sigsign.TimestampAuthorityOptions{
				URL:     timestampURL,
				Timeout: 30 * time.Second,
				Retries: 1,
			}))
		}
	}
	return bundleOptions, nil
}

func sigstoreSigningConfig(opts SigstoreSignOptions) (*sigroot.SigningConfig, error) {
	if opts.SigningConfig != "" {
		cfg, err := sigroot.NewSigningConfigFromPath(opts.SigningConfig)
		if err != nil {
			return nil, fmt.Errorf("load Sigstore signing config: %w", err)
		}
		return cfg, nil
	}
	if opts.FulcioURL != "" && (opts.NoRekor || opts.RekorURL != "") {
		return nil, nil
	}
	cfg, err := sigroot.FetchSigningConfigWithOptions(sigstoreSigningTUFOptions(opts))
	if err != nil {
		return nil, fmt.Errorf("fetch Sigstore signing config: %w", err)
	}
	return cfg, nil
}

func sigstoreSigningTrustedRoot(opts SigstoreSignOptions) (sigroot.TrustedMaterial, error) {
	if opts.TrustedRoot != "" {
		return sigroot.NewTrustedRootFromPath(opts.TrustedRoot)
	}
	trustedRoot, err := sigroot.FetchTrustedRootWithOptions(sigstoreSigningTUFOptions(opts))
	if err != nil {
		return nil, fmt.Errorf("fetch Sigstore trusted root: %w", err)
	}
	return trustedRoot, nil
}

func sigstoreSigningTUFOptions(opts SigstoreSignOptions) *sigtuf.Options {
	tufOptions := sigtuf.DefaultOptions()
	if opts.TUFCache != "" {
		tufOptions.WithCachePath(opts.TUFCache)
	}
	if opts.TUFURL != "" {
		tufOptions.WithRepositoryBaseURL(opts.TUFURL)
	}
	if opts.TUFStaging {
		tufOptions.WithRoot(sigtuf.StagingRoot())
		tufOptions.WithRepositoryBaseURL(sigtuf.StagingMirror)
	}
	return tufOptions
}

func sigstoreProvenanceStatement(manifestDigest string, cfg AgentConfig, attest string, createdAt time.Time) ([]byte, error) {
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
	return json.Marshal(statement)
}
