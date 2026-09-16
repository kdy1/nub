package store

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"testing"
)

func TestIntegrityAlgorithmsAndStrongestHash(t *testing.T) {
	data := []byte("hello world")
	h1 := sha1.Sum(data)
	h256 := sha256.Sum256(data)
	h384 := sha512.Sum384(data)
	h512 := sha512.Sum512(data)
	sris := []string{"sha1-" + base64.StdEncoding.EncodeToString(h1[:]), "sha256-" + base64.StdEncoding.EncodeToString(h256[:]), "sha384-" + base64.StdEncoding.EncodeToString(h384[:]), Integrity(data)}
	for _, sri := range sris {
		if err := Verify(data, sri); err != nil {
			t.Fatal(err)
		}
		if err := Verify([]byte("changed"), sri); err == nil {
			t.Fatal("bad content accepted", sri)
		}
	}
	wrong := Integrity([]byte("other"))
	for _, sri := range []string{sris[0] + " " + sris[3], sris[3] + " " + sris[0], wrong + " " + sris[3], sris[3] + "?foo=bar", "sha512-!!! " + sris[3]} {
		if err := Verify(data, sri); err != nil {
			t.Fatal(err)
		}
		if ok, err := VerifySHA512(h512, sri); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	for _, sri := range []string{sris[0] + " " + wrong, "sha512-!!!", "md5-unknown", "", wrong + " " + wrong} {
		if err := Verify(data, sri); err == nil {
			t.Fatal(sri)
		}
	}
	if ok, err := VerifySHA512(h512, sris[0]); err != nil || ok {
		t.Fatal("legacy digest should request buffered verification", ok, err)
	}
	if ok, err := VerifySHA512(h512, "sha512-AQ=="); err == nil || ok {
		t.Fatal(ok, err)
	}
}

func TestShasumConversion(t *testing.T) {
	hex := "dc96d6d3268bbc55103ce55cd4608d43d3b7ff72"
	sri, ok := ShasumToSRI("  " + hex + "\n")
	if !ok || sri != "sha1-3JbW0yaLvFUQPOVc1GCNQ9O3/3I=" {
		t.Fatal(sri, ok)
	}
	if got, ok := IntegrityHex(sri); !ok || got != hex {
		t.Fatal(got, ok)
	}
	for _, bad := range []string{"", "abc", "zz96d6d3268bbc55103ce55cd4608d43d3b7ff72"} {
		if _, ok := ShasumToSRI(bad); ok {
			t.Fatal(bad)
		}
	}
}

func TestContentIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, actual, want string
		valid              bool
	}{
		{"pkg", "1.0.0", "1.0.0", true}, {"pkg", "v1.0.0", "1.0.0", true}, {"pkg", "1.0.0+build", "1.0.0", true},
		{"pkg", "1.0.0+build", "1.0.0+different", false}, {"pkg", "1.0.0", "v1.0.0", false}, {"pkg", "vbeta", "beta", false},
		{"pkg", "0.0.0", "git+https://example/repo#hash", true}, {"pkg", "1.0.0", "file:../local", true}, {"other", "1.0.0", "1.0.0", false},
	} {
		body := []byte(fmt.Sprintf(`{"name":%q,"version":%q}`, tc.name, tc.actual))
		if err := ValidateContent(body, "pkg", tc.want); (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	if err := ValidateContent([]byte(`{"name":`), "pkg", "1.0.0"); err == nil {
		t.Fatal("invalid manifest accepted")
	}
}
