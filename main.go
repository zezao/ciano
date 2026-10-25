package main

import (
	"fmt"
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
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

	result := &current.Result{}
	return types.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "DEL network=%s containerID=%s netns=%s ifname=%s\n",
		conf.Name, args.ContainerID, args.Netns, args.IfName)
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
