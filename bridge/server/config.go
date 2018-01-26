package server

import "bridge/discovery"

type Config struct {
	Discovery *discovery.Config `yaml:"discovery"`
	Port      int               `yaml:"port"`
	Host      string            `yaml:"host"`
}

