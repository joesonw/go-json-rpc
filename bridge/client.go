package bridge

import (
	"bridge/client"
	"bridge/discovery"
	"fmt"
	"github.com/satori/go.uuid"
	"net/http"
	"reflect"
	"sync"
	"math/rand"
	"bytes"
	"io/ioutil"
	"encoding/json"
)

type Client struct {
	discovery      *discovery.Client
	serviceMapLock *sync.Mutex
	liveServices   map[uuid.UUID]*discovery.ClientInfo
	serviceMap     map[string]map[string][]uuid.UUID
	config         *client.Config
	http           *http.Client
}

func NewClient(config *client.Config) *Client {
	client := &Client{
		serviceMapLock: &sync.Mutex{},
		liveServices:   make(map[uuid.UUID]*discovery.ClientInfo),
		discovery:      discovery.NewClient(config.Discovery),
		serviceMap:     make(map[string]map[string][]uuid.UUID),
		config:         config,
		http:           &http.Client{

		},
	}

	go client.watch()
	return client
}

func (client* Client) Start() error {
	return client.discovery.Init()
}

func (client *Client) addService(info *discovery.ClientInfo) {
	if client.serviceMap[info.Name] == nil {
		client.serviceMap[info.Name] = make(map[string][]uuid.UUID)
	}
	if client.serviceMap[info.Name][info.Version] == nil {
		client.serviceMap[info.Name][info.Version] = make([]uuid.UUID, 0)
	}
	client.liveServices[info.ID] = info
	client.serviceMap[info.Name][info.Version] = append(client.serviceMap[info.Name][info.Version], info.ID)
}

func (client *Client) removeService(id uuid.UUID) {
	info := client.liveServices[id]
	if info == nil {
		return
	}

	serviceGroup := client.serviceMap[info.Name]
	if serviceGroup == nil {
		return
	}

	service := serviceGroup[info.Version]
	if service == nil {
		return
	}

	var result []uuid.UUID
	for _, serviceId := range service {
		if serviceId == id {
			continue
		}
		result = append(result, serviceId)
	}
	serviceGroup[info.Version] = result
}

func (client *Client) watch() {
	client.serviceMapLock.Lock()
	services := client.discovery.Services()
	for _, info := range services {
		client.addService(info)
	}
	client.serviceMapLock.Unlock()
	for event := range client.discovery.Subscribe() {
		info := event.Info
		client.serviceMapLock.Lock()
		if event.EventType == discovery.EventTypeOnline {
			client.addService(info)
		} else if event.EventType == discovery.EventTypeOffline {
			client.removeService(info.ID)
		}
		client.serviceMapLock.Unlock()
	}
}

func (client *Client) call(name string, version string, method string, args []interface{}, result interface{}) error {
	client.serviceMapLock.Lock()
	defer client.serviceMapLock.Unlock()
	serviceGroup := client.serviceMap[name]
	if serviceGroup == nil {
		return fmt.Errorf("No servers online")
	}

	service := serviceGroup[version]
	if service == nil || len(service) == 0 {
		return fmt.Errorf("No servers online")
	}

	index := rand.Intn(len(service))
	id := service[index]
	info := client.liveServices[id]
	if info == nil {
		return fmt.Errorf("No servers online")
	}

	requestBytes, err := json.Marshal(args)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s:%d/%s/%s/%s", info.Host, info.Port, name, version, method), bytes.NewReader(requestBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "applicatoin/json")
	res, err := client.http.Do(req)
	if err != nil {
		return err
	}


	responseBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}

	response := &Response{}
	response.Code = -1
	response.Data = result
	err = json.Unmarshal(responseBytes, response)
	if err != nil {
		return err
	}

	if response.Code == 0 {
		return nil
	} else if response.Code == 500 {
		return RemoteInternalError
	} else {
		return NewCodedError(response.Code, response.Message)
	}
}

func (client *Client) Use(in interface{}) error {
	v := reflect.ValueOf(in)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("remtoe serivce argument must be a pointer")
	}

	v = v.Elem()
	t := v.Type()
	et := t
	if et.Kind() == reflect.Ptr {
		et = et.Elem()
	}
	ptr := reflect.New(et)
	obj := ptr.Elem()

	name := ""
	version := ""

	if service, ok := (v.Interface()).(Service); ok {
		name = service.ServiceName()
		version = service.ServiceVersion()
	} else {
		return fmt.Errorf("must implement Service interface")
	}

	count := obj.NumField()

	for i := 0; i < count; i++ {
		f := obj.Field(i)
		ft := f.Type()
		sf := et.Field(i)
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		if f.CanSet() {
			fn := reflect.MakeFunc(ft, func(in []reflect.Value) (out []reflect.Value) {
				args := make([]interface{}, len(in))
				for i, arg := range in {
					args[i] = arg.Interface()
				}

				ot := ft.Out(0)

				result := reflect.New(ot)

				err := client.call(name, version, sf.Name, args, result.Interface())

				out = append(out, result.Elem(), reflect.ValueOf(&err).Elem())
				return
			})
			if f.Kind() == reflect.Ptr {
				fp := reflect.New(ft)
				fp.Elem().Set(fn)
				f.Set(fp)
			} else {
				f.Set(fn)
			}
		}
	}

	if t.Kind() == reflect.Ptr {
		v.Set(ptr)
	} else {
		v.Set(obj)
	}
	return nil
}
