package bridge

type Service interface {
	ServiceName() string
	ServiceVersion() string
}

type Response struct {
	Code    int         `json:"code"`
	Data    interface{} `json:"data"`
	Message string      `json:"message"`
}
