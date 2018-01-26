package bridge

import (
	"bridge/discovery"
	"bridge/server"
	"fmt"
	"github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"time"
	"encoding/json"
)

type ServiceStruct struct {
	Name     string
	Version  string
	ID       uuid.UUID
	Instance reflect.Value
	Methods  map[string]*ServiceMethod
}

type ServiceMethod struct {
	Name   string
	Method reflect.Method
	Args   []reflect.Type
}

type Server struct {
	config    *server.Config
	services  map[string]map[string]*ServiceStruct
	discovery *discovery.Client
}

func NewServer(config *server.Config) *Server {
	return &Server{
		config:   config,
		services: make(map[string]map[string]*ServiceStruct),
	}
}

func (server *Server) Register(service Service) error {
	if server.discovery == nil {
		server.discovery = discovery.NewClient(server.config.Discovery)
		if err := server.discovery.Init(); err != nil {
			return err
		}
	}

	name := service.ServiceName()
	version := service.ServiceVersion()
	id := uuid.NewV4()

	val := reflect.ValueOf(service)
	length := val.NumMethod()
	serv := &ServiceStruct{
		Name:     name,
		ID:       id,
		Version:  version,
		Instance: val,
		Methods:  make(map[string]*ServiceMethod),
	}

	for i := 0; i < length; i++ {
		method := val.Type().Method(i)
		if method.Name == "ServiceName" || method.Name == "ServiceVersion" {
			continue
		}
		if method.Type.NumOut() != 2 {
			return fmt.Errorf(`Service method must return exactly two arguments, first being result, second being value, "%s"`, method.Name)
		}
		args := make([]reflect.Type, method.Type.NumIn()-1)
		length := method.Type.NumIn()
		for i := 1; i < length; i++ {
			args[i-1] = method.Type.In(i)
		}
		serv.Methods[method.Name] = &ServiceMethod{
			Name:   method.Name,
			Args:   args,
			Method: method,
		}
	}

	if server.services[name] == nil {
		server.services[name] = make(map[string]*ServiceStruct)
	}
	server.services[name][version] = serv

	return server.discovery.Register(discovery.ClientInfo{
		ID:      id,
		Name:    name,
		Version: version,
		Host:    server.config.Host,
		Port:    server.config.Port,
	})
}

func (server *Server) Start() error {
	return http.ListenAndServe(fmt.Sprintf("%s:%d", server.config.Host, server.config.Port), server)
}

func (server *Server) ServeHTTP(res http.ResponseWriter, req *http.Request) {

	status := 500
	var body interface{}
	body = "Not Found"
	startOfTime := time.Now()
	var service *ServiceStruct
	requestID := uuid.NewV4()
	

	defer req.Body.Close()
	defer func() {
		res.Header().Set("X-Cost", fmt.Sprintf("%.2f", float64(time.Now().Sub(startOfTime).Nanoseconds())/float64(1000000)))
		res.Header().Set("X-Request-ID", requestID.String())
		if service != nil {
			res.Header().Set("X-Provider-ID", service.ID.String())
		}
		res.WriteHeader(200)
		if status == 500 {
			body = "Internal Error"
		}
		if status == 0 {
			bytes, _ := json.Marshal(&Response{
				Code: status,
				Data: body,
			})
			res.Write(bytes)
		} else {
			bytes, _ := json.Marshal(&Response{
				Code:    status,
				Message: body.(string),
			})
			res.Write(bytes)
		}
	}()

	if req.Method != http.MethodPost {
		return
	}

	keys := strings.Split(req.URL.Path, "/")
	if len(keys) != 4 {
		return
	}

	serviceGroup := server.services[keys[1]]
	if serviceGroup == nil {
		return
	}

	service = serviceGroup[keys[2]]
	if service == nil {
		return
	}

	method := service.Methods[keys[3]]
	if method == nil {
		return
	}

	requestBytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"service-id": service.ID.String(),
			"request-id": requestID.String(),
		}).Errorf("unable to read request body")
		return
	}

	methodArgs := make([]reflect.Value, len(method.Args)+1)
	methodArgs[0] = service.Instance
	requestArgs := make([]interface{}, len(method.Args))
	for i := range method.Args {
		arg := reflect.New(method.Args[i])
		requestArgs[i] = arg.Interface()
		methodArgs[i+1] = arg.Elem()
	}

	err = json.Unmarshal(requestBytes, &requestArgs)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"service-id": service.ID.String(),
			"request-id": requestID.String(),
		}).Errorf("unable to parse request body")
		return
	}

	results := method.Method.Func.Call(methodArgs)
	if results[1].Interface() != nil {
		err = results[1].Interface().(error)
		if IsCodedError(err) {
			codedError := err.(*CodedError)
			status = codedError.Code
			body = codedError.Message
		} else {
			logrus.WithError(err).Error("internal error")
			body = err.Error()
		}
	} else {
		body = results[0].Interface()
		status = 0
	}
}
