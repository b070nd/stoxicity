package config

import "testing"

func TestProviderQuotasAreExplicitAndValidated(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeConfig(t, "fetch:\n  quotas:\n    api.example.com: {requests: 5, period: 1m}\n"))
	if err != nil || cfg.Fetch.Quotas["api.example.com"].Requests != 5 {
		t.Fatalf("%+v %v", cfg.Fetch.Quotas, err)
	}
	for _, body := range []string{
		"requests: 0, period: 1m", "requests: -1, period: 1m", "requests: 5, period: 0s",
		"requests: 5, period: daily", "requests: 5, period: 9000h", "requests: 5, period: 1ns",
	} {
		if _, err := Load(writeConfig(t, "fetch:\n  quotas:\n    api.example.com: {"+body+"}\n")); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	for _, key := range []string{"https://a.test", "A.TEST", "secret@a.test", "api.example.com:443"} {
		if _, err := Load(writeConfig(t, "fetch:\n  quotas:\n    '"+key+"': {requests: 1, period: 1h}\n")); err == nil {
			t.Errorf("accepted key %s", key)
		}
	}
}
