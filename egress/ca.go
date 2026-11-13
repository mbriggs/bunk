package egress

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A CA signs the certificates the proxy shows a box for the hosts it
// inspects (see Policy.Paths). Every box trusts it: bunk puts its certificate
// on each box's identity drive. Its key stays on the host, so a box can't
// mint certificates of its own.
type CA struct {
	Cert []byte // the certificate, PEM, for boxes to trust

	cert *x509.Certificate
	key  crypto.Signer

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// LoadCA returns the CA in dir/egress-ca.pem, making it the first time.
func LoadCA(dir string) (*CA, error) {
	path := filepath.Join(dir, "egress-ca.pem")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if raw, err = newCA(); err != nil {
			return nil, err
		}
		// Linked into place, so two supervisors starting at once agree on
		// one CA: the second's link fails, and it reads the first's.
		tmp, err := os.CreateTemp(dir, ".egress-ca-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(raw); err != nil {
			return nil, err
		}
		tmp.Close()
		if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(raw, raw)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	return &CA{
		Cert:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		cert:   cert,
		key:    pair.PrivateKey.(crypto.Signer),
		leaves: map[string]*tls.Certificate{},
	}, nil
}

// newCA makes a CA's certificate and key, PEM, in one file.
func newCA() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "bunk egress"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...), nil
}

// leaf returns a certificate for host, signed by the CA.
func (ca *CA) leaf(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c := ca.leaves[host]; c != nil && time.Until(c.Leaf.NotAfter) > 24*time.Hour {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 30),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	ca.leaves[host] = c
	return c, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
