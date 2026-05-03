// Package gateway 实现 termind 跟 OpenClaw Gateway 的 WebSocket 长连接
// 以及跑在上面的 OpenClaw Gateway frame 消息层。
package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	frameTypeRequest  = "req"
	frameTypeResponse = "res"
	frameTypeEvent    = "event"
)

// frame 是 OpenClaw Gateway Protocol 的入站/出站统一外壳。
//
// 业务 Call/Notify 继续对上层暴露 method + params,但 wire format 是:
//   - request:  {type:"req", id, method, params}
//   - response: {type:"res", id, ok, payload|error}
//   - event:    {type:"event", event, payload, seq?, stateVersion?}
type frame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *frameError     `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
}

type frameError struct {
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	Details      json.RawMessage `json:"details,omitempty"`
	Retryable    bool            `json:"retryable,omitempty"`
	RetryAfterMS int             `json:"retryAfterMs,omitempty"`
}

func (e *frameError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// RPCError 是 gateway 对外暴露的错误类型。命名保留是为了不扰动上层调用点;
// 里面承载的是 OpenClaw response.error。
type RPCError struct {
	Code         string
	Message      string
	Details      json.RawMessage
	Retryable    bool
	RetryAfterMS int
}

func (e *RPCError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ConnectError 是 Gateway connect 阶段返回的 OpenClaw response.error。
type ConnectError struct {
	Code         string
	Message      string
	Details      json.RawMessage
	Retryable    bool
	RetryAfterMS int
}

type connectErrorDetails struct {
	Code            string `json:"code"`
	Reason          string `json:"reason"`
	RequestID       string `json:"requestId"`
	RemediationHint string `json:"remediationHint"`
}

func (e *ConnectError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ConnectError) detail() connectErrorDetails {
	var details connectErrorDetails
	if e != nil && len(e.Details) > 0 {
		_ = json.Unmarshal(e.Details, &details)
	}
	if details.Code == "" && strings.Contains(strings.ToLower(e.Message), "pairing required") {
		details.Code = "PAIRING_REQUIRED"
	}
	if details.RequestID == "" {
		details.RequestID = readRequestIDFromMessage(e.Message)
	}
	return details
}

func (e *ConnectError) DetailCode() string {
	return e.detail().Code
}

func (e *ConnectError) PairingRequestID() string {
	return e.detail().RequestID
}

func (e *ConnectError) PairingReason() string {
	return e.detail().Reason
}

func (e *ConnectError) RemediationHint() string {
	return e.detail().RemediationHint
}

func (e *ConnectError) IsPairingRequired() bool {
	return e != nil && e.DetailCode() == "PAIRING_REQUIRED"
}

func (e *ConnectError) IsAuthTokenMissing() bool {
	return e != nil && e.DetailCode() == "AUTH_TOKEN_MISSING"
}

func (e *ConnectError) IsAuthPasswordMissing() bool {
	return e != nil && e.DetailCode() == "AUTH_PASSWORD_MISSING"
}

func readRequestIDFromMessage(message string) string {
	start := strings.Index(strings.ToLower(message), "requestid:")
	if start < 0 {
		return ""
	}
	value := strings.TrimSpace(message[start+len("requestId:"):])
	value = strings.TrimRight(value, ")].,;")
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (m *frame) IsEvent() bool {
	return m.Type == frameTypeEvent && m.Event != ""
}

func (m *frame) IsRequest() bool {
	return m.Type == frameTypeRequest && m.ID != "" && m.Method != ""
}

func (m *frame) IsResponse() bool {
	return m.Type == frameTypeResponse && m.ID != ""
}

func encodeRequest(id, method string, params any) ([]byte, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	return json.Marshal(&frame{
		Type:   frameTypeRequest,
		ID:     id,
		Method: method,
		Params: rawParams,
	})
}

// encodeNotification keeps the Conn.Notify API, but OpenClaw has no
// notification frame. We send a req and intentionally do not wait for res.
func encodeNotification(id, method string, params any) ([]byte, error) {
	return encodeRequest(id, method, params)
}

func decode(raw []byte) (*frame, error) {
	var m frame
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode gateway frame: %w", err)
	}
	return &m, nil
}
