package main

import (
	"fmt"
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ipam"
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

	fmt.Fprintf(os.Stderr, "ADD containerID=%s netns=%s ifname=%s\n",
		args.ContainerID, args.Netns, args.IfName)
	fmt.Fprintf(os.Stderr, "config: name=%s bridge=%s isGateway=%t ipam=%s\n",
		conf.Name, conf.BrName, conf.IsGateway, conf.IPAM.Type)

	ipamResult, err := ipam.ExecAdd(conf.IPAM.Type, args.StdinData)
	if err != nil {
		return fmt.Errorf("failed to delegate IPAM to %q: %w", conf.IPAM.Type, err)
	}

	result, err := current.NewResultFromResult(ipamResult)
	if err != nil {
		return fmt.Errorf("failed to convert IPAM result: %w", err)
	}

	if len(result.IPs) == 0 {
		return fmt.Errorf("IPAM plugin %q returned no IP addresses", conf.IPAM.Type)
	}

	for _, ipc := range result.IPs {
		fmt.Fprintf(os.Stderr, "IPAM allocated ip=%s gateway=%v\n",
			ipc.Address.String(), ipc.Gateway)
	}

	return types.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "DEL network=%s containerID=%s netns=%s ifname=%s\n",
		conf.Name, args.ContainerID, args.Netns, args.IfName)

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
