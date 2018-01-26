package discovery

import (
	"context"
	"fmt"
	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/etcdserver/api/v3rpc/rpctypes"
	"github.com/coreos/etcd/mvcc/mvccpb"
	"github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
	"strings"
	"sync"
	"time"
	"encoding/json"
)

// Client bridge discovery client
type Client struct {
	etcd     *clientv3.Client
	lease    clientv3.Lease
	leaseID  clientv3.LeaseID
	clientID uuid.UUID
	config   *Config

	serviceRegistryWaitListLock *sync.Mutex
	serviceRegistryWaitList     map[uuid.UUID]ClientInfo
	liveServicesLock            *sync.Mutex
	liveServices                map[uuid.UUID]*ClientInfo
	subscriptions               []chan *ClientEvent
}

// NewClient new client
func NewClient(config *Config) *Client {
	return &Client{
		config:                      config,
		clientID:                    uuid.NewV4(),
		serviceRegistryWaitListLock: &sync.Mutex{},
		serviceRegistryWaitList:     make(map[uuid.UUID]ClientInfo),
		liveServicesLock:            &sync.Mutex{},
		liveServices:                make(map[uuid.UUID]*ClientInfo),
	}
}

// Init cilent must be inited first
func (client *Client) Init() error {
	var err error
	client.etcd, err = clientv3.New(clientv3.Config{
		Endpoints:   client.config.ETCD.Endpoints,
		DialTimeout: client.config.ETCD.Timeout,
	})
	if err != nil {
		return err
	}

	client.lease = clientv3.NewLease(client.etcd)

	err = client.keepAlive()
	if err != nil {
		return err
	}

	go client.keepAliveLoop()
	go client.watch()
	return nil
}

func (client *Client) keepAlive() error {
	var err error
	kv := clientv3.NewKV(client.etcd)
	client.serviceRegistryWaitListLock.Lock()
	defer client.serviceRegistryWaitListLock.Unlock()
	if client.leaseID == 0 { // if not lease id, create id first
		leaseID, err := client.lease.Grant(context.TODO(), int64(client.config.TTL.Seconds()))
		if err != nil {
			return err
		}
		for _, info := range client.serviceRegistryWaitList {
			bytes, err := json.Marshal(&info)
			if err != nil {
				return err
			}
			_, err = kv.Put(context.TODO(), fmt.Sprintf("/services/%s/%s/%s", info.Name, info.Version, info.ID), string(bytes), clientv3.WithLease(leaseID.ID))
			if err != nil {
				return err
			}
			logrus.Infof(`registered service "%s@%s" with id "%s"`, info.Name, info.Version, info.ID)
		}
		client.serviceRegistryWaitList = make(map[uuid.UUID]ClientInfo)
		client.leaseID = leaseID.ID
	} else {
		_, err = client.lease.KeepAliveOnce(context.TODO(), client.leaseID)
		if err != nil {
			if err == rpctypes.ErrLeaseNotFound {
				client.leaseID = 0
				return nil
			} else {
				return err
			}
		}
		for _, info := range client.serviceRegistryWaitList {
			bytes, err := json.Marshal(&info)
			if err != nil {
				return err
			}
			_, err = kv.Put(context.TODO(), fmt.Sprintf("/services/%s/%s/%s", info.Name, info.Version, info.ID), string(bytes), clientv3.WithLease(client.leaseID))
			if err != nil {
				return err
			}
			logrus.Infof(`registered service "%s@%s" with id "%s"`, info.Name, info.Version, info.ID)
		}
		client.serviceRegistryWaitList = make(map[uuid.UUID]ClientInfo)
	}
	return nil
}

func (client *Client) keepAliveLoop() {
	for range time.Tick(client.config.TTL - time.Millisecond*100) {
		if err := client.keepAlive(); err != nil {
			logrus.WithError(err).Errorf("unable to renew lease %d", client.leaseID)
		}
	}
}

// Register register client
func (client *Client) Register(info ClientInfo) error {
	client.serviceRegistryWaitListLock.Lock()
	defer client.serviceRegistryWaitListLock.Unlock()
	client.serviceRegistryWaitList[info.ID] = info
	return nil
}

func (client *Client) Subscribe() <-chan *ClientEvent {
	client.liveServicesLock.Lock()
	defer client.liveServicesLock.Unlock()
	events := make(chan *ClientEvent)
	client.subscriptions = append(client.subscriptions, events)
	return (<-chan *ClientEvent)(events)
}

func (client *Client) Services() map[uuid.UUID]*ClientInfo {
	client.liveServicesLock.Lock()
	defer client.liveServicesLock.Unlock()
	result := make(map[uuid.UUID]*ClientInfo)
	for k, v := range result {
		client.liveServices[k] = v
	}
	return result
}

func (client *Client) watch() {
	kv := clientv3.NewKV(client.etcd)
	var kvs []*mvccpb.KeyValue
	var rev int64
	{
		res, err := kv.Get(context.TODO(), "/services/", clientv3.WithPrefix(), clientv3.WithLimit(0))
		if err != nil {
			panic(err)
		}
		rev = res.Header.Revision
		kvs = append(kvs, res.Kvs...)
	}

	for _, kv := range kvs {
		info := &ClientInfo{}
		err := json.Unmarshal(kv.Value, info)
		if err != nil {
			logrus.WithError(err).Errorf("unable to parse json for client %s", string(kv.Key))
			continue
		}
		client.liveServicesLock.Lock()
		client.liveServices[info.ID] = info
		for _, events := range client.subscriptions {
			events <- &ClientEvent{
				Info:      info,
				EventType: EventTypeOnline,
			}
		}
		client.liveServicesLock.Unlock()
	}

	watcher := clientv3.NewWatcher(client.etcd)
	ch := watcher.Watch(context.TODO(), "/services/", clientv3.WithPrefix(), clientv3.WithRev(rev))
	for res := range ch {
		for _, event := range res.Events {
			keys := strings.Split(string(event.Kv.Key), "/")
			switch event.Type {
			case mvccpb.PUT:
				{
					info := &ClientInfo{}
					err := json.Unmarshal(event.Kv.Value, info)
					if err != nil {
						logrus.WithError(err).Errorf("unable to parse json for client %s", string(event.Kv.Key))
						continue
					}
					client.liveServicesLock.Lock()
					client.liveServices[info.ID] = info
					for _, events := range client.subscriptions {
						events <- &ClientEvent{
							Info:      info,
							EventType: EventTypeOnline,
						}
					}
					client.liveServicesLock.Unlock()
				}
			case mvccpb.DELETE:
				{
					id, err := uuid.FromString(keys[4])
					if err != nil {
						continue
					}
					info := &ClientInfo{
						ID: id,
					}
					client.liveServicesLock.Lock()
					delete(client.liveServices, info.ID)
					for _, events := range client.subscriptions {
						events <- &ClientEvent{
							Info:      info,
							EventType: EventTypeOffline,
						}
					}
					client.liveServicesLock.Unlock()
				}
			}
		}
	}
}
