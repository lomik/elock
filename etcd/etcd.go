package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"sync"
	"time"
)

type Request struct {
	method  string
	url     *url.URL
	timeout time.Duration
	ctx     context.Context
}

type Option func(r *Request)

func GET() Option {
	return func(r *Request) {
		r.method = "GET"
	}
}

func POST() Option {
	return func(r *Request) {
		r.method = "POST"
	}
}

func PUT() Option {
	return func(r *Request) {
		r.method = "PUT"
	}
}

func DELETE() Option {
	return func(r *Request) {
		r.method = "DELETE"
	}
}

func Wait(v bool) Option {
	return func(r *Request) {
		q := r.url.Query()

		if v {
			q.Set("wait", "true")
		} else {
			q.Del("wait")
		}

		r.url.RawQuery = q.Encode()
	}
}

func Value(value string) Option {
	return func(r *Request) {
		q := r.url.Query()
		q.Set("value", value)
		r.url.RawQuery = q.Encode()
	}
}

func PrevValue(value string) Option {
	return func(r *Request) {
		q := r.url.Query()
		q.Set("prevValue", value)
		r.url.RawQuery = q.Encode()
	}
}

func PrevIndex(index uint64) Option {
	return func(r *Request) {
		q := r.url.Query()
		q.Set("prevIndex", fmt.Sprintf("%d", index))
		r.url.RawQuery = q.Encode()
	}
}

func PrevExist(exists bool) Option {
	return func(r *Request) {
		q := r.url.Query()
		if exists {
			q.Set("prevExist", "true")
		} else {
			q.Set("prevExist", "false")
		}
		r.url.RawQuery = q.Encode()
	}
}

func TTL(ttl time.Duration) Option {
	return func(r *Request) {
		q := r.url.Query()
		q.Set("ttl", fmt.Sprintf("%d", int(ttl.Seconds())))
		r.url.RawQuery = q.Encode()
	}
}

func Refresh(v bool) Option {
	return func(r *Request) {
		q := r.url.Query()

		if v {
			q.Set("refresh", "true")
		} else {
			q.Del("refresh")
		}

		r.url.RawQuery = q.Encode()
	}
}

func WaitIndex(index uint64) Option {
	return func(r *Request) {
		q := r.url.Query()

		if index > 0 {
			q.Set("waitIndex", fmt.Sprintf("%d", index))
		} else {
			q.Del("waitIndex")
		}

		r.url.RawQuery = q.Encode()
	}
}

func Recursive(v bool) Option {
	return func(r *Request) {
		q := r.url.Query()

		if v {
			q.Set("recursive", "true")
		} else {
			q.Del("recursive")
		}

		r.url.RawQuery = q.Encode()
	}
}

func Sorted(v bool) Option {
	return func(r *Request) {
		q := r.url.Query()

		if v {
			q.Set("sorted", "true")
		} else {
			q.Del("sorted")
		}

		r.url.RawQuery = q.Encode()
	}
}

func Timeout(t time.Duration) Option {
	return func(r *Request) {
		r.timeout = t
	}
}

func Context(ctx context.Context) Option {
	return func(r *Request) {
		r.ctx = ctx
	}
}

type Node struct {
	Key           string     `json:"key"`
	Dir           bool       `json:"dir,omitempty"`
	Value         string     `json:"value"`
	Nodes         []*Node    `json:"nodes"`
	CreatedIndex  uint64     `json:"createdIndex"`
	ModifiedIndex uint64     `json:"modifiedIndex"`
	Expiration    *time.Time `json:"expiration,omitempty"`
	TTL           int64      `json:"ttl,omitempty"`
}

type Response struct {
	Action       string `json:"action"`
	Node         *Node  `json:"node"`
	PrevNode     *Node  `json:"prevNode"`
	ErrorCode    uint64 `json:"errorCode"`
	ErrorMessage string `json:"message"`
	ErrorCause   string `json:"cause"`
	Index        uint64 `json:"-"`
}

type Client struct {
	sync.RWMutex
	endpoints     []string
	endpointIndex int

	debug bool
}

func NewClient(endpoints []string, debug bool) (*Client, error) {
	// check endpoints parse
	for _, e := range endpoints {
		_, err := url.Parse(e)
		if err != nil {
			return nil, err
		}
	}

	return &Client{
		endpoints: endpoints,
		debug:     debug,
	}, nil
}

func (client *Client) Debug(format string, v ...interface{}) {
	if client.debug {
		log.Printf(format, v...)
	}
}

func (client *Client) currentEndpoint() string {
	client.RLock()
	res := client.endpoints[client.endpointIndex]
	client.RUnlock()
	return res
}

func (client *Client) nextEndpoint() string {
	client.Lock()
	client.endpointIndex = (client.endpointIndex + 1) % len(client.endpoints)
	client.Unlock()

	endpoint := client.currentEndpoint()
	client.Debug("new endpoint: %s", endpoint)

	return endpoint
}

func (client *Client) Query(key string, opts ...Option) (*Response, error) {
	endpoint := client.currentEndpoint()

QueryLoop:
	for {
		u, _ := url.Parse(endpoint) // error validated in client contructor
		u.Path = path.Join("/v2/keys", key)

		q := &Request{
			method:  "GET",
			url:     u,
			timeout: time.Minute,
			ctx:     context.Background(),
		}

		for _, o := range opts {
			o(q)
		}

		if q.ctx.Err() != nil {
			client.Debug("ctx.Err(): %s", q.ctx.Err().Error())
			return nil, q.ctx.Err()
		}

		client.Debug("%s %s", q.method, q.url.String())

		httpClient := &http.Client{
			Timeout: q.timeout,
		}

		// @TODO: check error?
		req, _ := http.NewRequest(q.method, q.url.String(), nil)
		req = req.WithContext(q.ctx)

		res, httpErr := httpClient.Do(req)
		body := []byte{}

		xEtcdIndex := ""

		if httpErr == nil {
			xEtcdIndex = res.Header.Get("X-Etcd-Index")

			body, httpErr = ioutil.ReadAll(res.Body)
			res.Body.Close()
		}

		// check context error
		if q.ctx.Err() != nil {
			client.Debug("context error: %#v", q.ctx.Err())
			return nil, q.ctx.Err()
		}

		if httpErr != nil {
			client.Debug("error: %s", httpErr.Error())
		} else {
			client.Debug("X-Etcd-Index: %s", xEtcdIndex)
			client.Debug("body: %s", string(body))
		}

		// reconnect on timeout, network error, endpoint down, etc
		if httpErr != nil {
			endpoint = client.nextEndpoint()
			time.Sleep(time.Second)
			continue QueryLoop
		}

		r := &Response{}
		err := json.Unmarshal(body, r)
		if err != nil {
			client.Debug("unmarshal error: %s", err.Error())
			return nil, err
		}

		index, _ := strconv.Atoi(xEtcdIndex)
		r.Index = uint64(index)

		client.Debug("response: %#v", r)

		return r, nil
	}
}
