package main

import "testing"

func TestLoadNetConfDefaults(t *testing.T) {
	conf, err := loadNetConf([]byte(`{"cniVersion":"1.0.0","name":"ciano","type":"ciano"}`))
	if err != nil {
		t.Fatalf("loadNetConf: %v", err)
	}
	if conf.BrName != "cni0" {
		t.Errorf("BrName = %q, want %q", conf.BrName, "cni0")
	}
	if conf.Name != "ciano" {
		t.Errorf("Name = %q, want %q", conf.Name, "ciano")
	}
	if conf.IsGateway {
		t.Error("IsGateway = true, want false")
	}
}

func TestLoadNetConfOverrides(t *testing.T) {
	conf, err := loadNetConf([]byte(`{
		"cniVersion": "1.0.0",
		"name": "ciano",
		"type": "ciano",
		"bridge": "br-test",
		"isGateway": true,
		"ipMasq": true,
		"mtu": 1400,
		"ipam": {"type": "host-local"}
	}`))
	if err != nil {
		t.Fatalf("loadNetConf: %v", err)
	}
	if conf.BrName != "br-test" {
		t.Errorf("BrName = %q, want %q", conf.BrName, "br-test")
	}
	if !conf.IsGateway {
		t.Error("IsGateway = false, want true")
	}
	if !conf.IPMasq {
		t.Error("IPMasq = false, want true")
	}
	if conf.MTU != 1400 {
		t.Errorf("MTU = %d, want 1400", conf.MTU)
	}
	if conf.IPAM.Type != "host-local" {
		t.Errorf("IPAM.Type = %q, want %q", conf.IPAM.Type, "host-local")
	}
}

func TestLoadNetConfInvalidJSON(t *testing.T) {
	if _, err := loadNetConf([]byte(`{`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
