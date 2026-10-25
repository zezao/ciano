package main

import (
	"fmt"
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

func main() {
	funcs := skel.CNIFuncs{
		Add:   cmdAdd,
		Del:   cmdDel,
		Check: cmdCheck,
	}
	skel.PluginMainFuncs(funcs, version.All, "ciano v0.1.0")
}

func cmdAdd(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if conf.IsDefaultGW {
		conf.IsGateway = true
	}

	fmt.Fprintf(os.Stderr, "ADD containerID=%s netns=%s ifname=%s\n",
		args.ContainerID, args.Netns, args.IfName)
	fmt.Fprintf(os.Stderr, "config: name=%s bridge=%s isGateway=%t ipam=%s mtu=%d\n",
		conf.Name, conf.BrName, conf.IsGateway, conf.IPAM.Type, conf.MTU)

	ipamResult, err := ipam.ExecAdd(conf.IPAM.Type, args.StdinData)
	if err != nil {
		return fmt.Errorf("failed to delegate IPAM to %q: %w", conf.IPAM.Type, err)
	}

	result, err := current.NewResultFromResult(ipamResult)
	if err != nil {
		_ = ipam.ExecDel(conf.IPAM.Type, args.StdinData)
		return fmt.Errorf("failed to convert IPAM result: %w", err)
	}
	if len(result.IPs) == 0 {
		_ = ipam.ExecDel(conf.IPAM.Type, args.StdinData)
		return fmt.Errorf("IPAM plugin %q returned no IP addresses", conf.IPAM.Type)
	}

	hostVethName := makeVethName(args.ContainerID, args.IfName)
	success := false
	defer func() {
		if success {
			return
		}
		if err := deleteLinkIfExists(hostVethName); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup: failed to delete veth %q: %v\n", hostVethName, err)
		}
		if err := ipam.ExecDel(conf.IPAM.Type, args.StdinData); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup: failed to release IPAM allocation: %v\n", err)
		}
	}()

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %w", args.Netns, err)
	}
	defer netns.Close()

	if conf.IPMasq {
		fmt.Fprintf(os.Stderr, "warning: ipMasq is not implemented yet\n")
	}

	gws := prepareResult(result, conf)

	br, err := ensureBridge(conf.BrName, conf.MTU)
	if err != nil {
		return err
	}

	if err := deleteLinkIfExists(hostVethName); err != nil {
		return fmt.Errorf("failed to remove stale veth %q: %w", hostVethName, err)
	}

	hostIface, contIface, err := createVeth(netns, hostVethName, args.IfName, conf.MTU)
	if err != nil {
		return err
	}
	result.CNIVersion = current.ImplementedSpecVersion
	result.Interfaces = []*current.Interface{
		{Name: br.Attrs().Name, Mac: br.Attrs().HardwareAddr.String()},
		hostIface,
		contIface,
	}

	if err := configureContainer(netns, args.IfName, result); err != nil {
		return err
	}

	if err := attachToBridge(hostVethName, br, conf.HairpinMode); err != nil {
		return err
	}

	for _, gw := range gws {
		if err := ensureBridgeAddr(br, gw.ipn); err != nil {
			return err
		}
		if err := enableIPForward(gw.family); err != nil {
			return err
		}
	}

	if link, err := netlink.LinkByName(conf.BrName); err == nil {
		result.Interfaces[0].Mac = link.Attrs().HardwareAddr.String()
	}

	success = true
	return types.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "DEL network=%s containerID=%s netns=%s ifname=%s\n",
		conf.Name, args.ContainerID, args.Netns, args.IfName)

	hostVethName := makeVethName(args.ContainerID, args.IfName)
	if err := deleteLinkIfExists(hostVethName); err != nil {
		return fmt.Errorf("failed to delete veth %q: %w", hostVethName, err)
	}

	if err := ipam.ExecDel(conf.IPAM.Type, args.StdinData); err != nil {
		return fmt.Errorf("failed to release IPAM allocation: %w", err)
	}
	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "CHECK network=%s containerID=%s netns=%s ifname=%s\n",
		conf.Name, args.ContainerID, args.Netns, args.IfName)
	return nil
}
