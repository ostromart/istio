// Copyright 2018 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handler

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"istio.io/pkg/log"
)

// stackDepth is used
// to determine how many levels of stack to skip
// it should be 1 if logging through a struct and
// it should be 2 if logging through an interface
// If depth=1 is used with an interface, filename appears as <autogenerated>
// logger is used through the adapter.ConsoleLogger interface.
const stackDepth = 2

var adapterScope = log.RegisterScope("adapters", "Messages from adapters", stackDepth)

type logger struct {
	name zapcore.Field
}

func newLogger(name string) logger {
	return logger{
		name: zap.String("adapter", name),
	}
}

// Infof from adapter.ConsoleLogger.
func (l logger) Infof(format string, args ...interface{}) {
	adapterScope.Info(fmt.Sprintf(format, args...), l.name)
}

// Warningf from adapter.ConsoleLogger.
func (l logger) Warningf(format string, args ...interface{}) {
	adapterScope.Warn(fmt.Sprintf(format, args...), l.name)
}

// Errorf from adapter.ConsoleLogger.
func (l logger) Errorf(format string, args ...interface{}) error {
	s := fmt.Sprintf(format, args...)
	adapterScope.Error(s, l.name)
	return errors.New(s)
}

// Debugf from adapter.ConsoleLogger.
func (l logger) Debugf(format string, args ...interface{}) {
	adapterScope.Debug(fmt.Sprintf(format, args...), l.name)
}

// InfoEnabled from adapter.ConsoleLogger.
func (l logger) InfoEnabled() bool {
	return adapterScope.InfoEnabled()
}

// WarnEnabled from adapter.ConsoleLogger.
func (l logger) WarnEnabled() bool {
	return adapterScope.WarnEnabled()
}

// ErrorEnabled from adapter.ConsoleLogger.
func (l logger) ErrorEnabled() bool {
	return adapterScope.ErrorEnabled()
}

// DebugEnabled from adapter.ConsoleLogger.
func (l logger) DebugEnabled() bool {
	return adapterScope.DebugEnabled()
}
