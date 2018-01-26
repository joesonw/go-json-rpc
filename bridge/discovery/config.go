package discovery

import (
	"github.com/satori/go.uuid"
	"time"
)

type ETCDConfig struct {
	Endpoints []string      `yaml:"endpoints"`
	Timeout   time.Duration `yaml:"timeout"`
}

type Config struct {
	ETCD *ETCDConfig   `yaml:"etcd"`
	TTL  time.Duration `yaml:"ttl"`
}

type ClientInfo struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Version string    `json:"version"`
	Host    string    `json:"host"`
	Port    int       `json:"port"`
}

type EventType int

const (
	EventTypeOnline EventType = 0
	EventTypeOffline
)

type ClientEvent struct {
	Info      *ClientInfo
	EventType EventType
}
