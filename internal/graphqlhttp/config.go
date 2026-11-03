package graphqlhttp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
)

func ParseSigningKey(hexValue string) ([]byte, error) {
	if hexValue == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		return secret, nil
	}
	secret, err := hex.DecodeString(hexValue)
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("COMMENG_SIGNING_KEY must be at least 32 bytes encoded as hex")
	}
	return secret, nil
}
func ParseTrustedProxy(cidr string) (*net.IPNet, error) {
	if cidr == "" {
		return nil, nil
	}
	_, network, err := net.ParseCIDR(cidr)
	return network, err
}
func ParseOrigins(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var result []string
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		urlValue, err := url.Parse(origin)
		if err != nil || urlValue.Host == "" ||
			(urlValue.Scheme != "http" && urlValue.Scheme != "https") ||
			urlValue.Path != "" || urlValue.User != nil ||
			urlValue.RawQuery != "" || urlValue.Fragment != "" {
			return nil, fmt.Errorf("invalid ALLOWED_ORIGINS value: %q", origin)
		}
		result = append(result, origin)
	}
	return result, nil
}
