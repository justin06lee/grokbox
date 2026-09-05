package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/justin06lee/grokbox/internal/proto"
)

// certLifetime is deliberately long. A self-signed certificate here is not a
// claim about identity that anyone else has to evaluate — the invite says
// which certificate to expect — so expiry buys nothing and an expired one
// would only break invites that were otherwise still good.
const certLifetime = 10 * 365 * 24 * time.Hour

// selfSigned loads the server's own certificate, generating one the first time
// and keeping it beside the room keys so that invites, which pin it, survive a
// restart. An empty dir keeps the certificate in memory, which is consistent
// with what a store-less server already does to room keys.
func selfSigned(dir string, hosts []string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if dir != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err == nil {
			return cert, proto.Fingerprint(cert.Certificate[0]), nil
		}
		if !os.IsNotExist(err) && !os.IsNotExist(rootCause(err)) {
			// A corrupt pair is worth saying out loud rather than silently
			// replacing: every invite already handed out pins the old one.
			return tls.Certificate{}, "", fmt.Errorf("cannot read %s: %w — delete it and the key beside it to start over, but every invite already handed out will stop working", certPath, err)
		}
	}

	certPEM, keyPEM, err := mintCert(hosts)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return tls.Certificate{}, "", err
		}
		if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
			return tls.Certificate{}, "", err
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
			return tls.Certificate{}, "", err
		}
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, proto.Fingerprint(cert.Certificate[0]), nil
}

func rootCause(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}

// mintCert produces a fresh self-signed certificate for the given hosts.
func mintCert(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "grokbox"},
		NotBefore:             now.Add(-time.Hour), // tolerate a skewed clock
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	// The names only matter to something that verifies the ordinary way — a
	// browser, or curl. A grokbox client checks the fingerprint instead.
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

// certHosts collects the names this server might be reached at, so a browser
// pointed at it has a chance of being satisfied too.
func certHosts(advertise string) []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if h := hostOf(advertise); h != "" {
		hosts = append(hosts, h)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return hosts
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		hosts = append(hosts, ipnet.IP.String())
	}
	return hosts
}
