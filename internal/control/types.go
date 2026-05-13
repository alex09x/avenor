package control

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      any        `json:"id"`
	Result  any        `json:"result,omitempty"`
	Error   *RespError `json:"error,omitempty"`
}

type RespError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func success(id any, result any) Response {
	return Response{JSONRPC: "2.0", ID: id, Result: result}
}

func failure(id any, code int, message string, data any) Response {
	return Response{JSONRPC: "2.0", ID: id, Error: &RespError{Code: code, Message: message, Data: data}}
}
