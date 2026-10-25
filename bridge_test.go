package main

import (
	"net"
	"strings"
	"testing"

	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/vishvananda/netlink"
)

func TestMakeVethName(t *testing.T) {
	name := makeVethName("container-1", "eth0")

	if !strings.HasPrefix(name, "veth") {
		t.Errorf("name = %q, want veth prefix", name)
	}
	if len(name) > 15 {
		t.Errorf("name = %q is %d chars, max is 15", name, len(name))
	}
	if name != makeVethName("container-1", "eth0") {
		t.Error("name is not deterministic")
	}
	if name == makeVethName("container-2", "eth0") {
		t.Error("different container IDs produced the same name")
	}
	if name == makeVethName("container-1", "eth1") {
		t.Error("different interface names produced the same name")
	}
}

func TestCalcGatewayIP(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"10.22.0.0/16", "10.22.0.1"},
		{"10.22.5.0/24", "10.22.5.1"},
		{"192.168.1.128/25", "192.168.1.129"},
	}

	for _, tc := range tests {
		_, ipn, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", tc.cidr, err)
		}
		if got := calcGatewayIP(ipn).String(); got != tc.want {
			t.Errorf("calcGatewayIP(%s) = %s, want %s", tc.cidr, got, tc.want)
		}
	}
}

func TestPrepareResult(t *testing.T) {
	addr, ipn, err := net.ParseCIDR("10.22.0.2/16")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	result := &current.Result{
		IPs: []*current.IPConfig{{
			Address: net.IPNet{IP: addr.To4(), Mask: ipn.Mask},
		}},
	}
	conf := &NetConf{IsGateway: true, IsDefaultGW: true}

	gws := prepareResult(result, conf)

	if len(gws) != 1 {
		t.Fatalf("got %d gateways, want 1", len(gws))
	}
	if got := gws[0].ipn.String(); got != "10.22.0.1/16" {
		t.Errorf("gateway = %s, want 10.22.0.1/16", got)
	}
	if gws[0].family != netlink.FAMILY_V4 {
		t.Errorf("family = %d, want FAMILY_V4", gws[0].family)
	}
	if got := result.IPs[0].Gateway.String(); got != "10.22.0.1" {
		t.Errorf("IP gateway = %s, want 10.22.0.1", got)
	}
	if len(result.Routes) != 1 || result.Routes[0].Dst.String() != "0.0.0.0/0" {
		t.Errorf("routes = %v, want one default route", result.Routes)
	}
	if result.IPs[0].Interface == nil || *result.IPs[0].Interface != 2 {
		t.Errorf("IP interface index = %v, want 2", result.IPs[0].Interface)
	}
}

func TestPrepareResultKeepsExistingDefaultRoute(t *testing.T) {
	addr, ipn, _ := net.ParseCIDR("10.22.0.2/16")
	result := &current.Result{
		IPs: []*current.IPConfig{{
			Address: net.IPNet{IP: addr.To4(), Mask: ipn.Mask},
		}},
		Routes: []*types.Route{{Dst: *defaultRoute(netlink.FAMILY_V4)}},
	}

	prepareResult(result, &NetConf{IsGateway: true, IsDefaultGW: true})

	if len(result.Routes) != 1 {
		t.Errorf("got %d routes, want 1 (no duplicate default route)", len(result.Routes))
	}
}
