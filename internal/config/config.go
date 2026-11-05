package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TextTokenBase64 string
	HubSpotToken    string
	RTMURL          string
	CDPURL          string
	AccountsURL     string
	HubSpotURL      string
	Port            string
	Interval        time.Duration
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func Load(listen bool) (Config, error) {
	c := Config{
		TextTokenBase64: env("TEXT_TOKEN_BASE64", ""),
		HubSpotToken:    env("HUBSPOT_ACCESS_TOKEN", ""),
		CDPURL:          env("TEXT_CDP_URL", "https://api.text.com/cdp"),
		AccountsURL:     env("TEXT_ACCOUNTS_URL", "https://accounts.text.com"),
		HubSpotURL:      env("HUBSPOT_URL", "https://api.hubapi.com"),
		RTMURL:          env("TEXT_RTM_URL", "wss://api.livechatinc.com/v3.6/agent/rtm/ws"),
		Port:            env("PORT", "8080"),
		Interval:        200 * time.Millisecond,
	}

	if c.TextTokenBase64 == "" || c.HubSpotToken == "" {
		return c, errors.New("TEXT_TOKEN_BASE64 and HUBSPOT_ACCESS_TOKEN are required")
	}

	decoded, err := base64.StdEncoding.DecodeString(c.TextTokenBase64)
	accountID, token, ok := strings.Cut(string(decoded), ":")

	if err != nil || !ok || accountID == "" || token == "" {
		return c, errors.New("TEXT_TOKEN_BASE64 must be Base64-encoded account_id:personal_access_token")
	}

	for _, entry := range []struct {
		name   string
		value  *string
		socket bool
	}{
		{"TEXT_CDP_URL", &c.CDPURL, false},
		{"TEXT_ACCOUNTS_URL", &c.AccountsURL, false},
		{"HUBSPOT_URL", &c.HubSpotURL, false},
		{"TEXT_RTM_URL", &c.RTMURL, true},
	} {
		u, e := url.Parse(*entry.value)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("%s must be an API base URL without credentials, query or fragment", entry.name)
		}

		secure, local := "https", "http"

		if entry.socket {
			secure, local = "wss", "ws"
		}

		loopback := u.Hostname() == "localhost"

		if ip := net.ParseIP(u.Hostname()); ip != nil {
			loopback = ip.IsLoopback()
		}

		if u.Scheme != secure && !(u.Scheme == local && loopback) {
			return c, fmt.Errorf("%s requires encrypted transport (loopback is allowed for tests)", entry.name)
		}

		*entry.value = strings.TrimRight(*entry.value, "/")
	}

	if raw := env("HTTP_MIN_INTERVAL", "200ms"); raw != "" {
		c.Interval, err = time.ParseDuration(raw)
		if err != nil || c.Interval < 0 || c.Interval > 30*time.Second {
			return c, errors.New("HTTP_MIN_INTERVAL must be between 0s and 30s")
		}
	}

	if listen {
		port, e := strconv.Atoi(c.Port)
		if e != nil || port < 1 || port > 65535 {
			return c, errors.New("PORT must be between 1 and 65535")
		}
	}

	return c, nil
}
