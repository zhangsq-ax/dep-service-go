package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/go-resty/resty/v2"
	"github.com/gorilla/websocket"
	jsoniter "github.com/json-iterator/go"
	"github.com/zhangsq-ax/logs"
	"go.uber.org/zap"
	"net/http"
	"sync"
	"time"
)

var (
	restyClients = sync.Map{}
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

func ResponseError(resBody []byte, successCode int) error {
	status := jsoniter.Get(resBody, "status").ToInt()
	message := jsoniter.Get(resBody, "message").ToString()
	if status != successCode {
		return fmt.Errorf("invalid status: %d - %s", status, message)
	}
	return nil
}

func ExtractResponseData[T any](resBody []byte, data T, path ...any) (T, error) {
	var zero T
	dataAny := jsoniter.Get(resBody, path...).GetInterface()
	if dataAny == nil {
		return zero, fmt.Errorf("no data in response: %s", string(resBody))
	}
	dataBytes, err := jsoniter.Marshal(dataAny)
	if err != nil {
		return zero, err
	}
	err = jsoniter.Unmarshal(dataBytes, data)
	return data, err
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
