package main

import (
	"encoding/json"
	"fmt"

	"github.com/containernetworking/cni/pkg/types"
)

type NetConf struct {
	types.NetConf
	BrName      string `json:"bridge"`
	IsGateway   bool   `json:"isGateway"`
	IsDefaultGW bool   `json:"isDefaultGateway"`
	IPMasq      bool   `json:"ipMasq"`
	MTU         int    `json:"mtu"`
	HairpinMode bool   `json:"hairpinMode"`
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{
		BrName: "cni0",
	}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %w", err)
	}
	return n, nil
}
