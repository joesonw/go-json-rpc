package client

import (
	"bridge/discovery"
)

type Config struct {
	Discovery *discovery.Config `yaml:"discovery"`
}


