package h2transport

import (
	"errors"
	"net/http"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/api"
	"golang.org/x/net/http2"
)

// A handler may call Goexit, which runs defers but cannot be recovered. Replace
// only that worker; task cleanup enqueues its reset before replacement starts.
func (c *candidateConnection) startWorker() {
	c.workers.Go(c.runWorker)
}

func (c *candidateConnection) runWorker() {
	returned := false
	defer func() {
		if !returned {
			c.startWorker()
		}
	}()
	c.work()
	returned = true
}

func (c *candidateConnection) work() {
	var active *requestTask
	defer func() {
		if active != nil {
			c.finishWork(active, nil, errResponse)
		}
	}()
	for task := range c.jobs {
		active = task
		result, err := c.execute(task)
		active = nil
		c.finishWork(task, result, err)
	}
}

func (c *candidateConnection) finishWork(task *requestTask, result *response, err error) {
	command := controlCommand{kind: responseCommand, streamID: task.id, response: result}
	if err != nil {
		command = controlCommand{kind: abortStreamCommand, streamID: task.id, code: http2.ErrCodeInternal}
		if errors.Is(err, errQueueFull) {
			command.code = http2.ErrCodeEnhanceYourCalm
		}
	}
	c.mu.Lock()
	task.response = result
	task.done = true
	c.releaseTask(task)
	c.mu.Unlock()
	if err := c.inbox.put(command); err != nil {
		_ = c.conn.Close()
	}
}

func (c *candidateConnection) execute(task *requestTask) (result *response, err error) {
	defer func() {
		if recovered := recover(); recovered != nil || err == errResponse {
			result.release()
			result, err = nil, errResponse
		}
	}()
	if err := task.ctx.Err(); err != nil {
		return nil, err
	}
	head := task.request.head
	if head.target == nil {
		return nil, errResponse
	}
	request, err := http.NewRequestWithContext(task.ctx, head.method, head.target.Scheme+"://"+head.authority, nil)
	if err != nil {
		return nil, err
	}
	// Reuse admission's parsed path, but keep a distinct URL for handler mutation.
	// Authority still goes through NewRequestWithContext's existing validation.
	target := head.target
	request.URL.Path, request.URL.RawPath, request.URL.RawQuery, request.URL.ForceQuery = target.Path, target.RawPath, target.RawQuery, target.ForceQuery
	request.RequestURI = head.path
	request.Proto, request.ProtoMajor, request.ProtoMinor = "HTTP/2.0", 2, 0
	request.Host, request.RemoteAddr = head.authority, c.conn.RemoteAddr().String()
	request.TLS = c.tlsState
	request.Body = api.NewBufferedBody(task.request.body)
	request.ContentLength = int64(len(task.request.body))
	for _, field := range head.fields {
		request.Header.Add(field.Name, field.Value)
	}
	if cookies := request.Header.Values("Cookie"); len(cookies) > 1 {
		request.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	if len(task.request.trailers) > 0 {
		request.Trailer = make(http.Header)
		for _, field := range task.request.trailers {
			request.Trailer.Add(field.Name, field.Value)
		}
	}
	result = &response{header: make(http.Header), limit: c.limits.MaxResponseBodyBytes, budget: c.budget}
	if request.Method == http.MethodHead {
		result.headBytes = -1
	}
	// Goexit leaves this failure marker in place while running cleanup defers.
	err = errResponse
	c.handler.ServeHTTP(result, request)
	if result.status == 0 {
		result.WriteHeader(http.StatusOK)
	}
	return result, result.err
}
