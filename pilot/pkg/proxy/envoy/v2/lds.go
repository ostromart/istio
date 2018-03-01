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
	"errors"

	xdsapi "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	"golang.org/x/net/context"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/log"
)

func (s *DiscoveryServer) StreamListeners(xdsapi.ListenerDiscoveryService_StreamListenersServer) error {
	return errors.New("StreamListeners not implemented")
}

func (s *DiscoveryServer) FetchListeners(ctx context.Context, in *xdsapi.DiscoveryRequest) (*xdsapi.DiscoveryResponse, error) {
	node, err := model.ParseServiceNode(in.Node.Id)
	if err != nil {
		return nil, err
	}
	log.Debugf("LDSv2 request for %s.", node.ID)
	return nil, errors.New("FetchListeners not implemented")
}
