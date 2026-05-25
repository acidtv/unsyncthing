package stclient

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGenerateCert(t *testing.T) {
	data, err := GenerateCert()
	if err != nil {
		t.Fatalf("GenerateCert() error: %v", err)
	}

	var result CertResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.CertPEM == "" {
		t.Error("CertPEM is empty")
	}
	if result.KeyPEM == "" {
		t.Error("KeyPEM is empty")
	}
	if result.DeviceID == "" {
		t.Error("DeviceID is empty")
	}

	// Syncthing device IDs are 8 groups of 7 chars separated by '-'.
	parts := strings.Split(result.DeviceID, "-")
	if len(parts) != 8 {
		t.Errorf("DeviceID has %d dash-separated parts, want 8: %s", len(parts), result.DeviceID)
	}

	block, _ := pem.Decode([]byte(result.CertPEM))
	if block == nil {
		t.Fatal("CertPEM is not valid PEM")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("PEM block type = %q, want CERTIFICATE", block.Type)
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		t.Errorf("parse certificate: %v", err)
	}

	keyBlock, _ := pem.Decode([]byte(result.KeyPEM))
	if keyBlock == nil {
		t.Fatal("KeyPEM is not valid PEM")
	}
	if keyBlock.Type != "EC PRIVATE KEY" {
		t.Errorf("KeyPEM block type = %q, want EC PRIVATE KEY", keyBlock.Type)
	}
}

func TestGenerateCert_UniqueEachCall(t *testing.T) {
	data1, err := GenerateCert()
	if err != nil {
		t.Fatal(err)
	}
	data2, err := GenerateCert()
	if err != nil {
		t.Fatal(err)
	}
	var r1, r2 CertResult
	json.Unmarshal(data1, &r1)
	json.Unmarshal(data2, &r2)
	if r1.DeviceID == r2.DeviceID {
		t.Error("two GenerateCert() calls returned the same DeviceID")
	}
}

func TestLoadCert_Valid(t *testing.T) {
	data, err := GenerateCert()
	if err != nil {
		t.Fatal(err)
	}
	var result CertResult
	json.Unmarshal(data, &result)

	cert, err := loadCert(result.CertPEM, result.KeyPEM)
	if err != nil {
		t.Fatalf("loadCert() error: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Error("loaded certificate chain is empty")
	}
}

func TestLoadCert_InvalidPEM(t *testing.T) {
	_, err := loadCert("not-valid-pem", "not-valid-pem")
	if err == nil {
		t.Error("loadCert() with invalid PEM should return an error")
	}
}

func TestLoadCert_MismatchedPair(t *testing.T) {
	data1, _ := GenerateCert()
	data2, _ := GenerateCert()
	var r1, r2 CertResult
	json.Unmarshal(data1, &r1)
	json.Unmarshal(data2, &r2)

	_, err := loadCert(r1.CertPEM, r2.KeyPEM)
	if err == nil {
		t.Error("loadCert() with mismatched cert/key should return an error")
	}
}
