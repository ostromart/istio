// Copyright 2017 Istio Authors
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

package v2

import (
	"sort"
	"time"
	// TODO(mostrowski): remove JSON encoding once mixer filter proto spec is available.
	oldjson "encoding/json"

	"github.com/envoyproxy/go-control-plane/api"
	"github.com/golang/protobuf/ptypes/duration"

	google_protobuf "github.com/gogo/protobuf/types"
)

// normalizeListeners sorts and de-duplicates listeners by address
func normalizeListeners(listeners []*api.Listener) []*api.Listener {
	out := make([]*api.Listener, 0, len(listeners))
	set := make(map[string]bool)
	for _, listener := range listeners {
		if !set[listener.Address.String()] {
			set[listener.Address.String()] = true
			out = append(out, listener)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address.String() < out[j].Address.String() })
	return out
}

// mustMarshalToString marshals i to a JSON string. It panics if i cannot be marshaled.
// TODO(mostrowski): this should be removed once v2 Mixer proto is finalized.
func mustMarshalToString(i interface{}) string {
	s, err := oldjson.Marshal(i)
	if err != nil {
		panic(err)
	}
	return string(s)
}

// BuildAddress returns a SocketAddress with the given ip and port.
func BuildAddress(ip string, port uint32) *api.Address {
	return &api.Address{
		Address: &api.Address_SocketAddress{
			SocketAddress: &api.SocketAddress{
				Address: ip,
				PortSpecifier: &api.SocketAddress_PortValue{
					PortValue: port,
				},
			},
		},
	}
}

// protoDurationToTimeDuration converts d to time.Duration format.
func protoDurationToTimeDuration(d *google_protobuf.Duration) time.Duration {
	return time.Duration(d.Nanos) + time.Second*time.Duration(d.Seconds)
}

// google_protobufToProto converts d to google protobuf Duration format.
func durationToProto(d time.Duration) *google_protobuf.Duration {
	nanos := d.Nanoseconds()
	secs := nanos / 1e9
	nanos -= secs * 1e9
	return &google_protobuf.Duration{
		Seconds: secs,
		Nanos:   int32(nanos),
	}
}

// durationToTimeDuration converts d to time.Duration format.
func durationToTimeDuration(d *duration.Duration) time.Duration {
	return time.Duration(d.Nanos) + time.Second*time.Duration(d.Seconds)
}
