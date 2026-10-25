package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type bridgeGateway struct {
	family int
	ipn    *net.IPNet
}

func makeVethName(containerID, ifName string) string {
	sum := sha256.Sum256([]byte(containerID + "/" + ifName))
	return "veth" + hex.EncodeToString(sum[:5])
}

func calcGatewayIP(ipn *net.IPNet) net.IP {
	return ip.NextIP(ipn.IP.Mask(ipn.Mask))
}

func ipFamily(addr net.IP) int {
	if addr.To4() != nil {
		return netlink.FAMILY_V4
	}
	return netlink.FAMILY_V6
}

func isDefaultRoute(dst *net.IPNet) bool {
	ones, _ := dst.Mask.Size()
	return ones == 0
}

func defaultRoute(family int) *net.IPNet {
	if family == netlink.FAMILY_V4 {
		return &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)}
	}
	return &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)}
}

func prepareResult(result *current.Result, conf *NetConf) []bridgeGateway {
	haveDefault := map[int]bool{}
	for _, route := range result.Routes {
		if isDefaultRoute(&route.Dst) {
			haveDefault[ipFamily(route.Dst.IP)] = true
		}
	}

	var gws []bridgeGateway
	for _, ipc := range result.IPs {
		family := ipFamily(ipc.Address.IP)
		if conf.IsGateway && ipc.Gateway == nil {
			ipc.Gateway = calcGatewayIP(&ipc.Address)
		}
		if conf.IsGateway && ipc.Gateway != nil {
			gws = append(gws, bridgeGateway{
				family: family,
				ipn:    &net.IPNet{IP: ipc.Gateway, Mask: ipc.Address.Mask},
			})
		}
		if conf.IsDefaultGW && !haveDefault[family] && ipc.Gateway != nil {
			result.Routes = append(result.Routes, &types.Route{
				Dst: *defaultRoute(family),
				GW:  ipc.Gateway,
			})
			haveDefault[family] = true
		}
		ipc.Interface = current.Int(2)
	}
	return gws
}

func ensureBridge(name string, mtu int) (*netlink.Bridge, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		attrs := netlink.NewLinkAttrs()
		attrs.Name = name
		attrs.MTU = mtu
		br := &netlink.Bridge{LinkAttrs: attrs}
		if err := netlink.LinkAdd(br); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("failed to create bridge %q: %w", name, err)
		}
		link, err = netlink.LinkByName(name)
		if err != nil {
			return nil, fmt.Errorf("failed to look up bridge %q: %w", name, err)
		}
	}

	br, ok := link.(*netlink.Bridge)
	if !ok {
		return nil, fmt.Errorf("link %q exists but is not a bridge", name)
	}
	if err := netlink.LinkSetUp(br); err != nil {
		return nil, fmt.Errorf("failed to set bridge %q up: %w", name, err)
	}
	return br, nil
}

func ensureBridgeAddr(br *netlink.Bridge, ipn *net.IPNet) error {
	addrs, err := netlink.AddrList(br, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("failed to list addresses on bridge %q: %w", br.Name, err)
	}
	for _, addr := range addrs {
		if addr.IPNet != nil && addr.IPNet.IP.Equal(ipn.IP) {
			return nil
		}
	}
	if err := netlink.AddrAdd(br, &netlink.Addr{IPNet: ipn}); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("failed to add %s to bridge %q: %w", ipn.String(), br.Name, err)
	}
	return nil
}

func deleteLinkIfExists(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil
		}
		return err
	}
	return netlink.LinkDel(link)
}

func createVeth(netns ns.NetNS, hostVethName, ifName string, mtu int) (*current.Interface, *current.Interface, error) {
	var hostIface, contIface *current.Interface
	err := netns.Do(func(hostNS ns.NetNS) error {
		hostVeth, contVeth, err := ip.SetupVethWithName(ifName, hostVethName, mtu, "", hostNS)
		if err != nil {
			return fmt.Errorf("failed to create veth pair: %w", err)
		}
		hostIface = &current.Interface{
			Name: hostVeth.Name,
			Mac:  hostVeth.HardwareAddr.String(),
		}
		contIface = &current.Interface{
			Name:    contVeth.Name,
			Mac:     contVeth.HardwareAddr.String(),
			Sandbox: netns.Path(),
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return hostIface, contIface, nil
}

func configureContainer(netns ns.NetNS, ifName string, result *current.Result) error {
	return netns.Do(func(_ ns.NetNS) error {
		if err := ipam.ConfigureIface(ifName, result); err != nil {
			return fmt.Errorf("failed to configure interface %q: %w", ifName, err)
		}
		return nil
	})
}

func attachToBridge(hostVethName string, br *netlink.Bridge, hairpin bool) error {
	hostVeth, err := netlink.LinkByName(hostVethName)
	if err != nil {
		return fmt.Errorf("failed to look up %q: %w", hostVethName, err)
	}
	if err := netlink.LinkSetMaster(hostVeth, br); err != nil {
		return fmt.Errorf("failed to attach %q to bridge %q: %w", hostVethName, br.Name, err)
	}
	if err := netlink.LinkSetHairpin(hostVeth, hairpin); err != nil {
		return fmt.Errorf("failed to configure hairpin mode on %q: %w", hostVethName, err)
	}
	if err := netlink.LinkSetUp(hostVeth); err != nil {
		return fmt.Errorf("failed to set %q up: %w", hostVethName, err)
	}
	return nil
}

func enableIPForward(family int) error {
	if family == netlink.FAMILY_V4 {
		if err := ip.EnableIP4Forward(); err != nil {
			return fmt.Errorf("failed to enable IPv4 forwarding: %w", err)
		}
		return nil
	}
	if err := ip.EnableIP6Forward(); err != nil {
		return fmt.Errorf("failed to enable IPv6 forwarding: %w", err)
	}
	return nil
}
