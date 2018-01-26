package bridge

import (
	"errors"
	"fmt"
)

var RemoteInternalError = errors.New("Remote Internal Error")

type CodedError struct {
	Code    int
	Message string
}

func (c *CodedError) Error() string {
	return fmt.Sprintf("Error(%d): %s", c.Code, c.Message)
}

func NewCodedError(code int, message string) error {
	return &CodedError{
		Code:    code,
		Message: message,
	}
}

func IsCodedError(err error) bool {
	_, ok := err.(*CodedError)
	return ok
}

func IsRemoteInternalError(err error) bool {
	return err == RemoteInternalError
}

