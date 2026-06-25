package osix

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sigbundle "github.com/sigstore/sigstore-go/pkg/bundle"
	sigroot "github.com/sigstore/sigstore-go/pkg/root"
	sigsign "github.com/sigstore/sigstore-go/pkg/sign"
	sigca "github.com/sigstore/sigstore-go/pkg/testing/ca"
	sigdata "github.com/sigstore/sigstore-go/pkg/testing/data"
)

const (
	sigstoreTestActionsIssuer = "https://token.actions.githubusercontent.com"
	sigstoreTestSANRegexp     = "^https://github.com/sigstore/sigstore-js/"
)

func TestVerifySigstorePublicGoodBundleWithCertificatePolicy(t *testing.T) {
	trustedRoot := sigdata.TrustedRoot(t, "public-good.json")
	entity := sigdata.Bundle(t, "sigstore.js@2.0.0-provenance.sigstore.json")
	digest, err := hex.DecodeString("46d4e2f74c4877316640000a6fdf8a8b59f1e0847667973e9859f774dd31b8f1e0937813b777fb66a2ac67d50540fe34640966eee9fc2ccca387082b4c85cd3c")
	if err != nil {
		t.Fatal(err)
	}
	opts := VerifyOptions{
		CertificateIdentityRegexp: sigstoreTestSANRegexp,
		CertificateOIDCIssuer:     sigstoreTestActionsIssuer,
	}
	result, err := verifySigstoreEntity(entity, opts, trustedRoot, "sha512", digest)
	if err != nil {
		t.Fatal(err)
	}
	if got := sigstoreSigner(result); got == "" || got == "sigstore-keyless" {
		t.Fatalf("expected certificate signer, got %q", got)
	}

	badIdentity := opts
	badIdentity.CertificateIdentityRegexp = ""
	badIdentity.CertificateIdentity = "https://github.com/smol-platform/not-this-repo"
	if _, err := verifySigstoreEntity(entity, badIdentity, trustedRoot, "sha512", digest); err == nil || !strings.Contains(err.Error(), "no matching CertificateIdentity") {
		t.Fatalf("expected certificate identity failure, got %v", err)
	}

	badDigest := append([]byte(nil), digest...)
	badDigest[0] ^= 0xff
	if _, err := verifySigstoreEntity(entity, opts, trustedRoot, "sha512", badDigest); err == nil {
		t.Fatalf("expected digest mismatch failure")
	}
}

func TestVerifySigstorePolicyRequiresIdentityAndIssuer(t *testing.T) {
	entity := sigdata.Bundle(t, "sigstore.js@2.0.0-provenance.sigstore.json")
	trustedRoot := sigdata.TrustedRoot(t, "public-good.json")
	digest := make([]byte, 64)
	_, err := verifySigstoreEntity(entity, VerifyOptions{CertificateOIDCIssuer: sigstoreTestActionsIssuer}, trustedRoot, "sha512", digest)
	if err == nil || !strings.Contains(err.Error(), "requires --certificate-identity") {
		t.Fatalf("expected missing identity policy failure, got %v", err)
	}
}

func TestSignedSnapshotEmitsSigstoreGoParsableBundles(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         root + "/fs",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root+"/fs/agent/workspace/sigstore.txt", "sigstore\n")
	snapshot, err := Snapshot(root, root+"/fs", SnapshotOptions{Tag: "snap-000001", Sign: "keyless", Attest: "slsa"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, refName := range []string{
		sigstoreSignatureBundleRefName(snapshot.ManifestDigest),
		sigstoreAttestationBundleRefName(snapshot.ManifestDigest),
	} {
		bundleDigest, err := s.resolveRef(refName)
		if err != nil {
			t.Fatal(err)
		}
		data, err := s.readBlob(bundleDigest)
		if err != nil {
			t.Fatal(err)
		}
		var parsed sigbundle.Bundle
		if err := parsed.UnmarshalJSON(data); err != nil {
			t.Fatalf("%s is not sigstore-go parsable: %v\n%s", refName, err, data)
		}
	}
}

func TestSnapshotSigstoreKeylessEmitsCertificateBundle(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "fs")
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         workspace,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(workspace, "agent", "workspace", "sigstore-public.txt"), "sigstore public\n")

	rootCert, rootKey, err := sigca.GenerateRootCa()
	if err != nil {
		t.Fatal(err)
	}
	intermediateCert, intermediateKey, err := sigca.GenerateFulcioIntermediate(rootCert, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	identity := "https://github.com/smol-platform/smol-agent-oci-fs/.github/workflows/release.yml@refs/heads/main"
	issuer := "https://token.actions.githubusercontent.com"
	oldFulcio := newSigstoreFulcio
	newSigstoreFulcio = func(*sigsign.FulcioOptions) sigsign.CertificateProvider {
		return testCertificateProvider{
			identity:         identity,
			issuer:           issuer,
			intermediateCert: intermediateCert,
			intermediateKey:  intermediateKey,
		}
	}
	defer func() { newSigstoreFulcio = oldFulcio }()

	snapshot, err := Snapshot(root, workspace, SnapshotOptions{
		Tag:    "snap-000001",
		Sign:   "sigstore-keyless",
		Attest: "slsa",
		Sigstore: SigstoreSignOptions{
			IdentityToken: "unused.test.token",
			FulcioURL:     "https://fulcio.test",
			NoRekor:       true,
			NoTimestamp:   true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	trustedRoot, err := sigroot.NewTrustedRoot(
		sigroot.TrustedRootMediaType01,
		[]sigroot.CertificateAuthority{&sigroot.FulcioCertificateAuthority{
			Root:                rootCert,
			Intermediates:       []*x509.Certificate{intermediateCert},
			ValidityPeriodStart: time.Now().Add(-time.Hour),
			ValidityPeriodEnd:   time.Now().Add(time.Hour),
			URI:                 "https://fulcio.test",
		}},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	trustedRootJSON, err := trustedRoot.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	trustedRootPath := filepath.Join(root, "trusted-root.json")
	if err := os.WriteFile(trustedRootPath, trustedRootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	verify, err := VerifySnapshot(root, snapshot.ManifestDigest, VerifyOptions{
		CertificateIdentity:          identity,
		CertificateOIDCIssuer:        issuer,
		SigstoreTrustedRoot:          trustedRootPath,
		SigstoreIgnoreTlog:           true,
		SigstoreIgnoreTimestamp:      true,
		SigstoreIgnoreCertificateSCT: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verify.SignatureDigest == "" || verify.ProvenanceDigest == "" || verify.Signer != identity {
		t.Fatalf("unexpected Sigstore verification result: %#v", verify)
	}
}

type testCertificateProvider struct {
	identity         string
	issuer           string
	intermediateCert *x509.Certificate
	intermediateKey  crypto.Signer
}

func (p testCertificateProvider) GetCertificate(_ context.Context, keypair sigsign.Keypair, _ *sigsign.CertificateProviderOptions) ([]byte, error) {
	publicKeyPEM, err := keypair.GetPublicKeyPem()
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("parse test public key PEM")
	}
	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:   big.NewInt(42),
		EmailAddresses: []string{p.identity},
		NotBefore:      now.Add(-time.Minute),
		NotAfter:       now.Add(10 * time.Minute),
		KeyUsage:       x509.KeyUsageDigitalSignature,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		ExtraExtensions: []pkix.Extension{{
			Id:       asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1},
			Critical: false,
			Value:    []byte(p.issuer),
		}},
	}
	return x509.CreateCertificate(rand.Reader, template, p.intermediateCert, publicKey, p.intermediateKey)
}
