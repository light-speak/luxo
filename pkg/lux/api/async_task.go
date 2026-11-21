package api

import (
	"github.com/light-speak/luxo/internal/panicerror"
	"github.com/light-speak/luxo/pkg/lux/luxolog"
)

// RunAsyncTask executes a task in the caller's goroutine. Generated async blocks
// invoke it inside their goroutine; it does not detach contexts, copy captures or manage
// capacity and shutdown. Abnormal exits are logged without invoking methods on
// panic payloads; they cannot change an already completed business response.
func RunAsyncTask(task func()) {
	returned := false
	defer func() {
		if returned {
			return
		}
		message := "async task exited without returning"
		if value := recover(); value != nil {
			message = "async task panic: " + panicerror.New("", value).Error()
		}
		luxolog.Error(message)
	}()
	task()
	returned = true
}
