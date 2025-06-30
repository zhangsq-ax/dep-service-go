package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/gorilla/websocket"
	jsoniter "github.com/json-iterator/go"
	"github.com/tidwall/gjson"
	"github.com/zhangsq-ax/logs"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

var (
	restyClients = sync.Map{}

	ErrInvalidStatus = fmt.Errorf("invalid status")
	ErrNoData        = fmt.Errorf("no data in response")
	ErrInvalidData   = fmt.Errorf("invalid data")
)

func GetRestyClient(baseUrl ...string) *resty.Client {
	var (
		url    = ""
		client *resty.Client
		ok     bool
		c      any
	)
	if len(baseUrl) > 0 {
		url = baseUrl[0]
	}
	if c, ok = restyClients.Load(url); !ok {
		client = resty.New().
			SetTransport(&http.Transport{
				MaxIdleConnsPerHost: 10,
			}).
			SetRetryCount(3).
			SetRetryWaitTime(5 * time.Second).
			OnBeforeRequest(func(c *resty.Client, r *resty.Request) error {
				logs.Infow("send-http-request", zap.String("method", r.Method), zap.String("url", url+r.URL), zap.Reflect("headers", r.Header), zap.Reflect("body", r.Body))
				return nil
			}).
			OnAfterResponse(func(c *resty.Client, r *resty.Response) error {
				logs.Infow("receive-http-response", zap.String("url", r.Request.URL), zap.Int("statusCode", r.StatusCode()), zap.ByteString("body", r.Body()))
				return nil
			})
		if url != "" {
			client.SetBaseURL(url)
		}
		restyClients.Store(url, client)
		c = client
	}
	client = c.(*resty.Client)
	return client
}

func ResponseError(resBody []byte, successCode ...int) error {
	results := gjson.GetManyBytes(resBody, "status", "message")
	status := int(results[0].Int())
	message := results[1].String()
	if len(successCode) == 0 {
		return nil
	}
	if !slices.Contains(successCode, status) {
		return fmt.Errorf("%w: %d - %s", ErrInvalidStatus, status, message)
	}
	return nil
}

func ExtractResponseData[T any](resBody []byte, data T, path ...string) (T, error) {
	var zero T
	if len(path) == 0 {
		path = []string{"data"}
	}
	result := gjson.GetBytes(resBody, path[0])
	if !result.Exists() {
		return zero, fmt.Errorf("%w: %s", ErrNoData, string(resBody))
	}
	err := jsoniter.Unmarshal([]byte(result.Raw), data)
	if err != nil {
		return zero, fmt.Errorf("%w: %s", ErrInvalidData, err.Error())
	}
	return data, nil
}

func SubscribeByWebSocket(ctx context.Context, url string, headers map[string]string, handler func(message []byte)) {
	var retryAfterSec time.Duration = 0
	header := http.Header{}
	if headers != nil {
		for k, v := range headers {
			header.Set(k, v)
		}
	}
CONNECT:
	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}
	conn, _, err := dialer.Dial(url, header)
	if err != nil {
		logs.Errorw("dial-websocket-failed", zap.String("url", url), zap.Reflect("headers", headers), zap.Error(err))
		if retryAfterSec == 60 {
			logs.Errorw("websocket-service-may-be-down", zap.String("url", url), zap.Reflect("headers", headers), zap.Error(err))
		}
		logs.Infow("reconnect-websocket", zap.Duration("after", retryAfterSec*time.Second), zap.String("url", url), zap.Reflect("headers", headers))
		time.Sleep(retryAfterSec * time.Second)
		goto RECONNECT
	}
	retryAfterSec = 0

	for {
		select {
		case <-ctx.Done():
			conn.Close()
			return
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				logs.Errorw("read-websocket-message-failed", zap.String("url", url), zap.Reflect("headers", headers), zap.Error(err))
				if retryAfterSec == 60 {
					logs.Errorw("websocket-service-may-be-down", zap.String("url", url), zap.Reflect("headers", headers), zap.Error(err))
				}
				logs.Infow("reconnect-websocket", zap.Duration("after", retryAfterSec*time.Second), zap.String("url", url), zap.Reflect("headers", headers))
				time.Sleep(retryAfterSec * time.Second)
				goto RECONNECT

			}
			retryAfterSec = 0
			handler(message)
		}
	}
RECONNECT:
	if retryAfterSec < 60 {
		retryAfterSec += 5
	}
	goto CONNECT
}
