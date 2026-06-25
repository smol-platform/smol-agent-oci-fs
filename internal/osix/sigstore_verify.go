package osix

import (
	"encoding/hex"
	"fmt"
	"strings"

	sigbundle "github.com/sigstore/sigstore-go/pkg/bundle"
	sigroot "github.com/sigstore/sigstore-go/pkg/root"
	sigtuf "github.com/sigstore/sigstore-go/pkg/tuf"
	sigverify "github.com/sigstore/sigstore-go/pkg/verify"
)

func sigstorePolicyActive(opts VerifyOptions) bool {
	return opts.CertificateIdentity != "" ||
		opts.CertificateIdentityRegexp != "" ||
		opts.CertificateOIDCIssuer != "" ||
		opts.CertificateOIDCIssuerRegexp != "" ||
		opts.SigstoreTrustedRoot != "" ||
		opts.SigstoreTUFCache != "" ||
		opts.SigstoreTUFURL != "" ||
		opts.SigstoreTUFStaging ||
		opts.SigstoreIgnoreTlog ||
		opts.SigstoreIgnoreTimestamp ||
		opts.SigstoreIgnoreCertificateSCT
}

func verifySigstoreSnapshot(s store, digest string, opts VerifyOptions) (VerifyResult, error) {
	bundleDigest, err := s.resolveRef(sigstoreSignatureBundleRefName(digest))
	if err != nil {
		return VerifyResult{}, fmt.Errorf("sigstore signature bundle not found for %s: %w", digest, err)
	}
	bundleData, err := s.readBlob(bundleDigest)
	if err != nil {
		return VerifyResult{}, err
	}
	rawDigest, err := digestRawBytes(digest)
	if err != nil {
		return VerifyResult{}, err
	}
	trustedMaterial, err := sigstoreTrustedMaterial(opts)
	if err != nil {
		return VerifyResult{}, err
	}
	result, err := verifySigstoreBundleData(bundleData, opts, trustedMaterial, "sha256", rawDigest)
	if err != nil {
		return VerifyResult{}, err
	}
	provDigest, _ := s.resolveRef(sigstoreAttestationBundleRefName(digest))
	return VerifyResult{
		ManifestDigest:   digest,
		SignatureDigest:  bundleDigest,
		ProvenanceDigest: provDigest,
		Signer:           sigstoreSigner(result),
	}, nil
}

func verifySigstoreBundleData(bundleData []byte, opts VerifyOptions, trustedMaterial sigroot.TrustedMaterial, digestAlgorithm string, digest []byte) (*sigverify.VerificationResult, error) {
	var entity sigbundle.Bundle
	if err := entity.UnmarshalJSON(bundleData); err != nil {
		return nil, fmt.Errorf("parse sigstore bundle: %w", err)
	}
	return verifySigstoreEntity(&entity, opts, trustedMaterial, digestAlgorithm, digest)
}

func verifySigstoreEntity(entity sigverify.SignedEntity, opts VerifyOptions, trustedMaterial sigroot.TrustedMaterial, digestAlgorithm string, digest []byte) (*sigverify.VerificationResult, error) {
	if !hasCertificateIdentityPolicy(opts) {
		return nil, fmt.Errorf("sigstore keyless verification requires --certificate-identity or --certificate-identity-regexp and --certificate-oidc-issuer or --certificate-oidc-issuer-regexp")
	}
	certID, err := sigverify.NewShortCertificateIdentity(
		opts.CertificateOIDCIssuer,
		opts.CertificateOIDCIssuerRegexp,
		opts.CertificateIdentity,
		opts.CertificateIdentityRegexp,
	)
	if err != nil {
		return nil, fmt.Errorf("build sigstore certificate identity policy: %w", err)
	}
	verifierOptions := sigstoreVerifierOptions(opts)
	verifier, err := sigverify.NewVerifier(trustedMaterial, verifierOptions...)
	if err != nil {
		return nil, fmt.Errorf("build sigstore verifier: %w", err)
	}
	result, err := verifier.Verify(entity, sigverify.NewPolicy(
		sigverify.WithArtifactDigest(strings.ToLower(digestAlgorithm), digest),
		sigverify.WithCertificateIdentity(certID),
	))
	if err != nil {
		return nil, fmt.Errorf("sigstore bundle verification failed: %w", err)
	}
	return result, nil
}

func hasCertificateIdentityPolicy(opts VerifyOptions) bool {
	hasIdentity := opts.CertificateIdentity != "" || opts.CertificateIdentityRegexp != ""
	hasIssuer := opts.CertificateOIDCIssuer != "" || opts.CertificateOIDCIssuerRegexp != ""
	return hasIdentity && hasIssuer
}

func sigstoreVerifierOptions(opts VerifyOptions) []sigverify.VerifierOption {
	var verifierOptions []sigverify.VerifierOption
	if !opts.SigstoreIgnoreTlog {
		verifierOptions = append(verifierOptions, sigverify.WithTransparencyLog(1))
	}
	if opts.SigstoreIgnoreTimestamp {
		verifierOptions = append(verifierOptions, sigverify.WithCurrentTime())
	} else {
		verifierOptions = append(verifierOptions, sigverify.WithObserverTimestamps(1))
	}
	if !opts.SigstoreIgnoreCertificateSCT {
		verifierOptions = append(verifierOptions, sigverify.WithSignedCertificateTimestamps(1))
	}
	return verifierOptions
}

func sigstoreTrustedMaterial(opts VerifyOptions) (sigroot.TrustedMaterial, error) {
	if opts.SigstoreTrustedRoot != "" {
		return sigroot.NewTrustedRootFromPath(opts.SigstoreTrustedRoot)
	}
	tufOptions := sigtuf.DefaultOptions()
	if opts.SigstoreTUFCache != "" {
		tufOptions.WithCachePath(opts.SigstoreTUFCache)
	}
	if opts.SigstoreTUFURL != "" {
		tufOptions.WithRepositoryBaseURL(opts.SigstoreTUFURL)
	}
	if opts.SigstoreTUFStaging {
		tufOptions.WithRoot(sigtuf.StagingRoot())
		tufOptions.WithRepositoryBaseURL(sigtuf.StagingMirror)
	}
	trustedRoot, err := sigroot.FetchTrustedRootWithOptions(tufOptions)
	if err != nil {
		return nil, fmt.Errorf("fetch sigstore trusted root: %w", err)
	}
	return trustedRoot, nil
}

func sigstoreSigner(result *sigverify.VerificationResult) string {
	if result == nil {
		return "sigstore-keyless"
	}
	if result.Signature != nil && result.Signature.Certificate != nil {
		if result.Signature.Certificate.SubjectAlternativeName != "" {
			return result.Signature.Certificate.SubjectAlternativeName
		}
	}
	if result.VerifiedIdentity != nil {
		if result.VerifiedIdentity.SubjectAlternativeName.SubjectAlternativeName != "" {
			return result.VerifiedIdentity.SubjectAlternativeName.SubjectAlternativeName
		}
		if regex := result.VerifiedIdentity.SubjectAlternativeName.Regexp.String(); regex != "" {
			return regex
		}
	}
	if result.Signature != nil && result.Signature.PublicKeyID != nil {
		return "sigstore-key:" + hex.EncodeToString(*result.Signature.PublicKeyID)
	}
	return "sigstore-keyless"
}
