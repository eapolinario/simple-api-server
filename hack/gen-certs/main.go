/*
Copyright 2026 Eduardo Apolinario.
*/

// gen-certs is a tiny tool that generates a self-signed CA and a
// serving certificate for the aggregated apiserver, used by the e2e
// harness in place of cert-manager. It exists so hack/e2e-up.sh can
// stay in bash without growing openssl-flag handling.
//
// Output layout (under -out):
//
//	ca.crt           PEM-encoded CA certificate (also used as caBundle)
//	ca.key           PEM-encoded CA private key
//	tls.crt          PEM-encoded serving cert (chain: leaf only — the
//	                 aggregator trusts the CA directly via caBundle, so
//	                 we don't need to ship the chain)
//	tls.key          PEM-encoded serving key
//
// Filenames `tls.crt` / `tls.key` match what `kubectl create secret
// tls` expects, so the bring-up script can hand the directory straight
// to kubectl with --cert / --key.
//
// Idempotence: if all four files already exist under -out, the tool is
// a no-op. This is deliberate — re-running e2e-up.sh should not
// invalidate the in-cluster Secret or the APIService caBundle.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func main() {
	var (
		outDir   = flag.String("out", "", "directory to write certs into (required)")
		validFor = flag.Duration("valid-for", 365*24*time.Hour, "cert validity window")
		dnsSANs  = stringSlice{
			"simple-apiserver",
			"simple-apiserver.simple-apiserver",
			"simple-apiserver.simple-apiserver.svc",
			"simple-apiserver.simple-apiserver.svc.cluster.local",
		}
	)
	flag.Var(&dnsSANs, "dns-san", "additional DNS SAN (repeatable); defaults cover the in-cluster Service")
	flag.Parse()

	if *outDir == "" {
		log.Fatal("--out is required")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	paths := certPaths(*outDir)
	if allExist(paths) {
		log.Printf("certs already present under %s; nothing to do", *outDir)
		return
	}

	if err := generate(paths, *validFor, dnsSANs); err != nil {
		log.Fatalf("generate: %v", err)
	}
	log.Printf("wrote ca + serving cert to %s", *outDir)
}

type certFiles struct {
	caCrt, caKey, tlsCrt, tlsKey string
}

func certPaths(dir string) certFiles {
	return certFiles{
		caCrt:  filepath.Join(dir, "ca.crt"),
		caKey:  filepath.Join(dir, "ca.key"),
		tlsCrt: filepath.Join(dir, "tls.crt"),
		tlsKey: filepath.Join(dir, "tls.key"),
	}
}

func allExist(p certFiles) bool {
	for _, f := range []string{p.caCrt, p.caKey, p.tlsCrt, p.tlsKey} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

func generate(p certFiles, validFor time.Duration, dnsSANs []string) error {
	// CA. P-256 keys are smaller and faster than RSA-2048 and Go's
	// crypto/tls supports them everywhere; kube-apiserver's aggregator
	// has no objection.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("gen ca key: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "simple-apiserver-e2e-ca"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(validFor),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign ca: %w", err)
	}

	// Serving cert, signed by the CA above. DNS SANs only — the
	// aggregator dials the Service by name, never by IP.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("gen leaf key: %w", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: dnsSANs[0]},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsSANs,
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return fmt.Errorf("parse ca: %w", err)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign leaf: %w", err)
	}

	if err := writeCertPEM(p.caCrt, caDER); err != nil {
		return err
	}
	if err := writeECKeyPEM(p.caKey, caKey); err != nil {
		return err
	}
	if err := writeCertPEM(p.tlsCrt, leafDER); err != nil {
		return err
	}
	return writeECKeyPEM(p.tlsKey, leafKey)
}

func serial() *big.Int {
	// 128-bit serials per CA/Browser Forum guidance; overkill for a
	// throwaway e2e CA but cheap.
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	return n
}

func writeCertPEM(path string, der []byte) error {
	return writePEM(path, &pem.Block{Type: "CERTIFICATE", Bytes: der}, 0o644)
}

func writeECKeyPEM(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal ec key: %w", err)
	}
	return writePEM(path, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}, 0o600)
}

func writePEM(path string, block *pem.Block, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, block); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// stringSlice is a repeatable string flag.
type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}
