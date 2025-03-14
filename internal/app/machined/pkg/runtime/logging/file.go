// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/siderolabs/gen/containers"
	"github.com/siderolabs/go-tail"

	"github.com/cozystack/talm/internal/app/machined/pkg/runtime"
	"github.com/siderolabs/talos/pkg/follow"
)

// FileLoggingManager implements simple logging to files.
type FileLoggingManager struct {
	logDirectory string

	registeredLogs containers.ConcurrentMap[string, struct{}]
}

// NewFileLoggingManager initializes new FileLoggingManager.
func NewFileLoggingManager(logDirectory string) *FileLoggingManager {
	return &FileLoggingManager{
		logDirectory: logDirectory,
	}
}

// ServiceLog implements runtime.LoggingManager interface.
func (manager *FileLoggingManager) ServiceLog(id string) runtime.LogHandler {
	return &fileLogHandler{
		logDirectory: manager.logDirectory,
		id:           id,
		manager:      manager,
	}
}

// SetSenders implements runtime.LoggingManager interface (by doing nothing).
func (manager *FileLoggingManager) SetSenders([]runtime.LogSender) []runtime.LogSender {
	return nil
}

// RegisteredLogs implements runtime.LoggingManager interface.
func (manager *FileLoggingManager) RegisteredLogs() []string {
	var result []string

	manager.registeredLogs.ForEach(func(key string, _ struct{}) {
		result = append(result, key)
	})

	return result
}

type fileLogHandler struct {
	path string

	logDirectory string
	id           string
	manager      *FileLoggingManager
}

func (handler *fileLogHandler) buildPath() error {
	if strings.ContainsAny(handler.id, string(os.PathSeparator)+".") {
		return errors.New("service ID is invalid")
	}

	handler.path = filepath.Join(handler.logDirectory, handler.id+".log")

	return nil
}

// Writer implements runtime.LogHandler interface.
func (handler *fileLogHandler) Writer() (io.WriteCloser, error) {
	if err := handler.buildPath(); err != nil {
		return nil, err
	}

	result, err := os.OpenFile(handler.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return nil, err
	}

	handler.manager.registeredLogs.GetOrCreate(handler.id, struct{}{})

	return result, nil
}

// Reader implements runtime.LogHandler interface.
func (handler *fileLogHandler) Reader(opts ...runtime.LogOption) (io.ReadCloser, error) {
	var opt runtime.LogOptions

	for _, o := range opts {
		if err := o(&opt); err != nil {
			return nil, err
		}
	}

	if err := handler.buildPath(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(handler.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	if opt.TailLines != nil {
		err = tail.SeekLines(f, *opt.TailLines)
		if err != nil {
			f.Close() //nolint:errcheck

			return nil, fmt.Errorf("error tailing log: %w", err)
		}
	}

	if opt.Follow {
		return follow.NewReader(context.Background(), f), nil
	}

	return f, nil
}
