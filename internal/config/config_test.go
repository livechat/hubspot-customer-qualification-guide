package config

import (
	"encoding/base64"
	"testing"
)

func TestImportDoesNotRequireListenerConfiguration(t *testing.T) {
	t.Setenv("TEXT_TOKEN_BASE64", base64.StdEncoding.EncodeToString([]byte("account:synthetic")))
	t.Setenv("HUBSPOT_ACCESS_TOKEN", "synthetic")
	t.Setenv("PORT", "not-a-port")

	if _, err := Load(false); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(true); err == nil {
		t.Fatal("listener accepted an invalid port")
	}
}

func TestTextTokenRequiresEncodedAccountAndPAT(t *testing.T) {
	t.Setenv("HUBSPOT_ACCESS_TOKEN", "synthetic")

	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{"encoded credentials", base64.StdEncoding.EncodeToString([]byte("account:synthetic")), true},
		{"raw credentials", "account:synthetic", false},
		{"encoded PAT only", base64.StdEncoding.EncodeToString([]byte("synthetic")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEXT_TOKEN_BASE64", tc.value)

			_, err := Load(false)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
}

func TestRejectsUnsafeAPIURLs(t *testing.T) {
	t.Setenv("TEXT_TOKEN_BASE64", base64.StdEncoding.EncodeToString([]byte("account:synthetic")))
	t.Setenv("HUBSPOT_ACCESS_TOKEN", "synthetic")

	for _, value := range []string{
		"http://remote.example",
		"https://user:password@example.com",
		"https://example.com?token=secret",
		"https://example.com/#secret",
	} {
		for _, name := range []string{"TEXT_CDP_URL", "TEXT_ACCOUNTS_URL"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				t.Setenv(name, value)

				if _, err := Load(false); err == nil {
					t.Fatalf("accepted unsafe URL %s", value)
				}
			})
		}
	}
}
