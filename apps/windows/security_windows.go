package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Secrets struct {
	Token    string `json:"token"`
	User     string `json:"user"`
	Password string `json:"password"`
	CertPEM  []byte `json:"cert"`
	KeyPEM   []byte `json:"key"`
}

func randomSecret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func protect(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty credential data")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func loadSecrets(dir string) (*Secrets, error) {
	p := filepath.Join(dir, "credentials.dpapi")
	b, e := os.ReadFile(p)
	if e == nil {
		plain, e := protect(b, true)
		if e != nil {
			return nil, e
		}
		var s Secrets
		if e = json.Unmarshal(plain, &s); e != nil {
			return nil, e
		}
		if len(s.Token) < 32 || len(s.Password) < 32 {
			return nil, errors.New("invalid stored credentials")
		}
		return &s, nil
	}
	if !os.IsNotExist(e) {
		return nil, e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, e
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "DDRemote private companion"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, DNSNames: []string{"localhost"}}
	der, e := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if e != nil {
		return nil, e
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return nil, e
	}
	s := &Secrets{Token: randomSecret(), User: "ddremote", Password: randomSecret(), CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), KeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}
	return s, saveSecrets(dir, s)
}

func saveSecrets(dir string, s *Secrets) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	b, e = protect(b, false)
	if e != nil {
		return e
	}
	p := filepath.Join(dir, "credentials.dpapi")
	tmp := p + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, p)
}
func (s *Secrets) certificate() (tls.Certificate, string, error) {
	c, e := tls.X509KeyPair(s.CertPEM, s.KeyPEM)
	if e != nil {
		return c, "", e
	}
	sum := sha256.Sum256(c.Certificate[0])
	return c, hex.EncodeToString(sum[:]), nil
}
